//go:build windows

package localtty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsPTY 是 Windows 上的本地 PTY:ConPTY(pseudoconsole)提供终端
// 语义(ANSI 渲染、窗口尺寸、行编辑回显),conhost 把 shell 的
// stdout+stderr 合并成一路 UTF-8 字节流。对外语义与 unixPTY 一致。
//
// 与 Unix 的差别:Win32 没有从端、会话和前台进程组,也没有可投递的
// 信号,所以尺寸变化走 ResizePseudoConsole,Signal 只把少数终止信号
// 映射成 TerminateProcess。
type windowsPTY struct {
	// mu 串行化句柄访问:shell 自己退出时 waitLoop 要关掉伪控制台和
	// 进程句柄,而 UI 线程可能同时在 Resize/Signal/Kill —— 不加锁就会
	// 触碰到已释放(甚至已被系统复用)的句柄。
	mu      sync.Mutex
	hpc     windows.Handle
	closed  bool
	waitErr error

	// pi 在 startPTY 里写一次之后不再改动,waitLoop 用它等进程结束。
	pi windows.ProcessInformation

	// in/out 是父进程侧的管道端点:in 写 shell 的 stdin,out 读
	// shell 的 stdout+stderr。两端都是同步句柄,所以能直接包成 os.File
	// 做阻塞读写。
	in  *os.File
	out *os.File

	closeOnce sync.Once
	closeErr  error

	done chan struct{}
}

// startPTY 用 ConPTY 建一对管道加一个伪控制台,再把 shell 作为"客户端"
// 挂上去。成功后父进程只留下两个端点(写输入 / 读输出),另外两端由
// 伪控制台持有。
func startPTY(cfg ptyConfig) (ptyHandle, error) {
	// 必须用 windows.CreatePipe 而不是 os.Pipe:os.Pipe 在 Go 里是
	// overlapped 句柄,而 CreatePseudoConsole 只接受同步管道,否则直接
	// 失败(ERROR_INVALID_PARAMETER)。
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to create local shell input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, fmt.Errorf("failed to create local shell output pipe: %w", err)
	}
	// closePipes 收掉父进程手里的 4 个管道端点,只在失败路径上用。
	closePipes := func() {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		windows.CloseHandle(outW)
	}

	// 伪控制台读 inR、写 outW;父进程留 inW 写输入、outR 读输出。
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(ptySize(cfg.cols, cfg.rows), inR, outW, 0, &hpc); err != nil {
		closePipes()
		return nil, fmt.Errorf("failed to create pseudo console: %w", err)
	}
	// 伪控制台建立之后,失败路径还要连它一起关掉。
	abort := func(err error) (ptyHandle, error) {
		windows.ClosePseudoConsole(hpc)
		closePipes()
		return nil, err
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return abort(fmt.Errorf("failed to allocate proc thread attribute list: %w", err))
	}
	// 属性列表引用的内存要活到 CreateProcess 内部拷贝完成,所以在这里
	// 才 Delete(本函数返回时已经过了 CreateProcess)。
	defer attrs.Delete()

	// 属性值就是 HPCON 句柄本身,不是它的地址:微软官方 ConPTY 样例
	// 也是按值传入,传 &hpc 会让 CreateProcess 直接失败。
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, handleValue(hpc), unsafe.Sizeof(hpc)); err != nil {
		return abort(fmt.Errorf("failed to set pseudo console attribute: %w", err))
	}

	si := windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attrs.List(),
	}
	// 三个标准句柄必须显式设成 INVALID_HANDLE_VALUE,并带上
	// STARTF_USESTDHANDLES:这等于告诉控制台子系统"用子进程自己控制台
	// 的句柄",内核会把它们替换成伪控制台的标准句柄。
	//
	// 这里踩过一个真实的坑:三个句柄留空(0)时 Windows 会把父进程的
	// 标准句柄原样复制给子进程。gossh 作为服务器运行时自己的 stdout
	// 往往是管道或重定向文件,于是 shell 的输出直接写进了服务器的
	// stdout,伪控制台一个字节都收不到 —— 会话里只剩 attach/teardown
	// 转义序列,读端很快就 EOF。实测把 bInheritHandles 改成 true 也
	// 一样漏,只有 INVALID_HANDLE_VALUE 能让子进程真正挂到伪控制台上。
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = windows.InvalidHandle
	si.StdOutput = windows.InvalidHandle
	si.StdErr = windows.InvalidHandle

	appName, err := windows.UTF16PtrFromString(cfg.shell)
	if err != nil {
		return abort(wrapStartError(cfg.shell, err))
	}
	// 命令行里必须带 argv[0]:CreateProcess 只按第一个空白切分 token,
	// 少了程序名 shell 会把第一个参数当成自己的名字(cmd.exe 会打印
	// 用法而不是执行命令)。
	argv := make([]string, 0, len(cfg.args)+1)
	argv = append(argv, cfg.shell)
	argv = append(argv, cfg.args...)
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return abort(wrapStartError(cfg.shell, err))
	}
	env, err := envBlock(cfg.env)
	if err != nil {
		return abort(wrapStartError(cfg.shell, err))
	}
	// 工作目录为空时传 nil,让子进程继承服务器进程的当前目录。
	var cwd *uint16
	if cfg.dir != "" {
		if cwd, err = windows.UTF16PtrFromString(cfg.dir); err != nil {
			return abort(wrapStartError(cfg.shell, err))
		}
	}

	var pi windows.ProcessInformation
	// bInheritHandles=false:管道端点由伪控制台属性转交,子进程不需要
	// (也不该)继承服务器进程的其他句柄。
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(appName, cmdline, nil, nil, false, flags, env, cwd, &si.StartupInfo, &pi); err != nil {
		return abort(wrapStartError(cfg.shell, err))
	}
	// 子进程已经连上伪控制台:inR/outW 归伪控制台所有。父进程再留着
	// 它们就等于自己攥住写端,shell 退出后 outR 永远等不到 EOF,
	// 阻塞在 Read 上的会话输出协程会一直挂着。
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	// 线程句柄用不上,立刻释放,避免会话一多就泄漏句柄。
	_ = windows.CloseHandle(pi.Thread)

	p := &windowsPTY{
		hpc:  hpc,
		pi:   pi,
		in:   os.NewFile(uintptr(inW), "|conpty-in"),
		out:  os.NewFile(uintptr(outR), "|conpty-out"),
		done: make(chan struct{}),
	}
	go p.waitLoop()
	return p, nil
}

// ptySize 把会话的列/行换算成 ConPTY 的 COORD(字符格子、int16)。
// 缺省或超出 int16 的尺寸回落到默认几何,与 Unix 端的初始尺寸语义一致。
func ptySize(cols, rows int) windows.Coord {
	if cols <= 0 || cols > 0x7fff {
		cols = defaultCols
	}
	if rows <= 0 || rows > 0x7fff {
		rows = defaultRows
	}
	return windows.Coord{X: int16(cols), Y: int16(rows)}
}

// handleValue 把句柄的位模式当作 unsafe.Pointer 交给
// UpdateProcThreadAttribute:该 API 对 PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE
// 取的是"值"而不是地址,但形参类型是 unsafe.Pointer,没法直接传句柄。
// 这里也不能写成 unsafe.Pointer(hpc) —— 那是 uintptr→Pointer 转换,
// go vet 会判为误用;经由指针做类型双关拿到的是同一个位模式,语义相同。
func handleValue(h windows.Handle) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&h))
}

// envBlock 把 "K=V" 列表编码成 CreateProcess 需要的 UTF-16 环境块:
// 每项以 NUL 结尾,整块再补一个 NUL 收尾(CREATE_UNICODE_ENVIRONMENT
// 要求的就是 UTF-16 块)。env 为空时返回 nil,让子进程继承父进程环境。
func envBlock(env []string) (*uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	block := make([]uint16, 0, len(env)*8+1)
	for _, kv := range env {
		// 没有 '=' 的项不是环境变量,直接跳过(Windows 内部的盘符当前
		// 目录变量形如 "=C:=C:\...",它以 '=' 开头但仍含 '=',会保留)。
		if !strings.Contains(kv, "=") {
			continue
		}
		entry, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, fmt.Errorf("invalid environment entry %q: %w", kv, err)
		}
		block = append(block, entry...)
	}
	if len(block) == 0 {
		return nil, nil
	}
	block = append(block, 0)
	return &block[0], nil
}

// waitLoop 等 shell 结束,然后立刻收掉伪控制台。会话的输出协程多半正
// 阻塞在 outR 的 Read 上,只有伪控制台关闭(conhost 退出 → 管道写端
// 消失)才能让它拿到 EOF,否则 Tty 的读循环会一直挂到超时。
func (p *windowsPTY) waitLoop() {
	_, waitErr := windows.WaitForSingleObject(p.pi.Process, windows.INFINITE)
	var code uint32
	if waitErr == nil {
		if err := windows.GetExitCodeProcess(p.pi.Process, &code); err != nil {
			waitErr = err
		}
	}
	// 先释放句柄再暴露退出状态:release 会等未完成的 Read 收尾,于是
	// Wait 返回时输出流也已经结束。
	_ = p.Close()

	p.mu.Lock()
	switch {
	case waitErr != nil:
		p.waitErr = waitErr
	case code != 0:
		p.waitErr = fmt.Errorf("local shell exited with code %d", code)
	}
	p.mu.Unlock()
	close(p.done)
}

// Read 返回 shell 输出。shell 退出或 Close 之后管道会断开,Windows 报
// ERROR_BROKEN_PIPE / ERROR_OPERATION_ABORTED / ERROR_INVALID_HANDLE,
// Close 之后还可能是 os.ErrClosed;统一归一成 EOF,让会话输出协程干净
// 收尾(与 Unix 端把主端 EIO 翻成 EOF 同理)。
func (p *windowsPTY) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if err != nil && !errors.Is(err, io.EOF) && isPTYGone(err) {
		return n, io.EOF
	}
	return n, err
}

// Write 把会话输入送进 shell 的 stdin(行编辑与回显由 ConPTY 负责)。
func (p *windowsPTY) Write(b []byte) (int, error) { return p.in.Write(b) }

// Resize 直接改伪控制台的尺寸,conhost 会让挂在上面的程序收到窗口变化
// (等价于 Unix 的 SIGWINCH)。
func (p *windowsPTY) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrExited
	}
	return windows.ResizePseudoConsole(p.hpc, ptySize(cols, rows))
}

// Signal 处理终止类信号。Win32 没有 POSIX 信号,伪控制台也不提供"把
// 信号投递给前台进程组"的能力,所以终止信号退化成 TerminateProcess;
// 其余信号(如 SIGALRM)在 Windows 上没有对应语义,静默忽略并返回
// nil —— 这里绝不能阻塞,否则 Tty.Close 的宽限逻辑会卡住。
func (p *windowsPTY) Signal(sig syscall.Signal) error {
	if p.Exited() {
		return ErrExited
	}
	switch sig {
	case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL:
		return p.Kill()
	default:
		return nil
	}
}

// Kill 强杀 shell(只杀进程本身,conhost 由 Close 收尾)。幂等:进程
// 已经不在了就返回 nil,便于 Close 的超时分支重复调用。
func (p *windowsPTY) Kill() error {
	if p.Exited() {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if err := windows.TerminateProcess(p.pi.Process, 1); err != nil {
		// 与 Exited() 之间进程刚好退出时,TerminateProcess 对已终止的
		// 进程会返回 ERROR_ACCESS_DENIED —— 结果和成功一样。
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil
		}
		return err
	}
	return nil
}

// PID 返回 shell 的进程 id。
func (p *windowsPTY) PID() int { return int(p.pi.ProcessId) }

// Wait 阻塞到 shell 退出,退出码为 0 时返回 nil。
func (p *windowsPTY) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

// Exited 不阻塞,可以在任意协程调用。
func (p *windowsPTY) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Close 幂等:第一次真正释放句柄,之后每次都返回同一个结果。句柄值一旦
// 被系统复用,二次关闭就会误伤别的句柄,所以这里既不能重复关,也不能
// 让并发调用者绕过标记。
func (p *windowsPTY) Close() error {
	p.closeOnce.Do(p.release)

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeErr
}

// release 释放伪控制台、管道端点和进程句柄,只会执行一次。
func (p *windowsPTY) release() {
	p.mu.Lock()
	hpc := p.hpc
	proc := p.pi.Process
	p.hpc = 0
	p.closed = true
	p.mu.Unlock()

	// 1. 先关伪控制台:conhost 随之退出,输出管道的写端彻底消失,
	//    阻塞在 outR 上的 Read 因此拿到 EOF。
	// 2. 再关管道端点:关 outR 时会等那次 Read 收尾,上一步已经保证
	//    它会返回,所以这里不会死等。
	// 3. 最后关进程句柄:此刻 Kill/Signal 在 closed 标记前已经止步,
	//    不会再用到它。
	if hpc != 0 {
		windows.ClosePseudoConsole(hpc)
	}
	var err error
	if p.in != nil {
		err = errors.Join(err, p.in.Close())
	}
	if p.out != nil {
		err = errors.Join(err, p.out.Close())
	}
	if proc != 0 {
		err = errors.Join(err, windows.CloseHandle(proc))
	}

	p.mu.Lock()
	p.closeErr = err
	p.mu.Unlock()
}

// isPTYGone 判断错误是不是"PTY 已经不存在"——管道断开或句柄已关闭。
func isPTYGone(err error) bool {
	return errors.Is(err, windows.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windows.ERROR_OPERATION_ABORTED) ||
		errors.Is(err, windows.ERROR_INVALID_HANDLE) ||
		errors.Is(err, windows.ERROR_HANDLE_EOF) ||
		errors.Is(err, os.ErrClosed)
}
