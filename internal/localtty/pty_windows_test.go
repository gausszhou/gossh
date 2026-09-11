//go:build windows

package localtty

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// conptySentinel 是成功路径的唯一标记:cmd.exe 会回显整条命令,但
	// 标记字面量只有 echo 的结果里才有。
	conptySentinel = "GOSSH_CONPTY_OK"
	// conptyEnvName 用来验证 cfg.env 真的被编码成环境块传给了 shell。
	conptyEnvName = "GOSSH_PTY_PROBE"

	// 真机起 ConPTY 偶尔要等上几百毫秒,给足超时,失败信息里也能看出
	// 是超时而不是断言不成立。宁可超时长一点,也不要让测试假绿。
	conptyExitTimeout = 30 * time.Second
	conptyReadTimeout = 15 * time.Second
)

// ansiEscapes 匹配 ConPTY 输出里的控制序列:CSI(光标定位/清屏/颜色)、
// OSC(窗口标题)以及单字符转义。ConPTY 会在文本之间插入光标定位序列,
// 所以断言前先剥掉它们,但仍然按子串断言输出内容本身。
var ansiEscapes = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// syncBuffer 收集输出协程读到的字节:读协程写、测试协程读,必须加锁。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// cmdExe 返回系统目录里的 cmd.exe:测试要用真实的 Windows shell,而不是
// 依赖 PATH 或 %COMSPEC% 在当前环境里的取值。
func cmdExe(t *testing.T) string {
	t.Helper()
	system32, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatalf("GetSystemDirectory: %v", err)
	}
	return filepath.Join(system32, "cmd.exe")
}

// startTestShell 起一个真实的 ConPTY shell,并在后台把输出收进缓冲。
func startTestShell(t *testing.T, env []string, args ...string) (ptyHandle, *syncBuffer, <-chan struct{}) {
	t.Helper()
	p, err := startPTY(ptyConfig{
		shell: cmdExe(t),
		args:  args,
		dir:   t.TempDir(),
		env:   env,
		cols:  defaultCols,
		rows:  defaultRows,
	})
	if err != nil {
		t.Fatalf("startPTY: %v", err)
	}
	if pid := p.PID(); pid <= 0 {
		t.Fatalf("PID() = %d, want > 0", pid)
	}

	out := &syncBuffer{}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = io.Copy(out, p)
	}()
	return p, out, readDone
}

// waitForExit 等 Wait 返回,并断言退出码为 0。
func waitForExit(t *testing.T, p ptyHandle) {
	t.Helper()
	if err := waitForExitErr(t, p); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
}

// waitForExitErr 带超时地等 Wait 返回:死等会让整个测试挂住,超时才能
// 把"输出协程没被唤醒/进程没收尾"这种 bug 变成一条失败信息。
func waitForExitErr(t *testing.T, p ptyHandle) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(conptyExitTimeout):
		t.Fatalf("Wait() did not return within %s", conptyExitTimeout)
		return nil
	}
}

// awaitReader 确认输出协程在 Wait 之后收敛(读到 EOF)。不等它结束就
// 断言缓冲区,可能读到还没写完的内容。
func awaitReader(t *testing.T, readDone <-chan struct{}) {
	t.Helper()
	select {
	case <-readDone:
	case <-time.After(conptyReadTimeout):
		t.Fatalf("output reader did not stop within %s after the shell exited", conptyReadTimeout)
	}
}

// stripANSI 去掉控制序列,只留可见文本。
func stripANSI(s string) string { return ansiEscapes.ReplaceAllString(s, "") }

// procGetProcessHandleCount 不在 x/sys/windows 的绑定里,按需从 kernel32
// 取(只在测试里用)。
var procGetProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")

// openHandleCount 返回当前进程的句柄总数。
func openHandleCount(t *testing.T) uint32 {
	t.Helper()
	var count uint32
	r1, _, err := procGetProcessHandleCount.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
	if r1 == 0 {
		t.Skipf("GetProcessHandleCount unavailable: %v", err)
	}
	return count
}

// settledHandleCount 采样当前进程的句柄总数,并在短暂窗口内等它稳定下来。
//
// 为什么不能只读一次:同一个测试进程里先跑过的测试(TestTtyRunsShellAndResizes
// 等)会真起 shell,它们的进程/伪控制台句柄由系统异步释放,可能在某一轮采样时
// 恰好被计入,表现为一次性的跳变(实测会在某一轮突然跳 +6/+12,位置不固定)。
// 真实泄漏的特征是「每轮都稳定增长」,与这种一次性抖动完全不同。连续两次读数
// 一致即认为已稳定;若一直在涨(真泄漏)则等满窗口后返回,增量照样会被断言抓到。
func settledHandleCount(t *testing.T) uint32 {
	t.Helper()
	prev := openHandleCount(t)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		now := openHandleCount(t)
		if now == prev {
			return now
		}
		prev = now
	}
	return prev
}

// runTestShell 跑完一次 echo 会话:等退出、读干净输出、关掉句柄。
func runTestShell(t *testing.T) {
	t.Helper()
	p, _, readDone := startTestShell(t, os.Environ(), "/c", "echo "+conptySentinel)
	waitForExit(t, p)
	awaitReader(t, readDone)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWindowsPTYNoHandleLeak 反复建/关会话:Close 若漏掉伪控制台或进程
// 句柄,句柄数会随轮次线性增长(每轮至少漏三个),一眼就能看出来。
//
// 判据是「增长是否持续」,而不是首尾总量的差值。实测:句柄总数会在某一轮
// 一次性跳 +6/+12 并保持(单独复现时分别落在第 3 轮、第 0 轮或不出现),
// 这不是 Close 漏句柄——真漏的话每一轮都会漏——而是同进程里其它测试真起
// shell 后的异步回收、runtime/DLL 惰性分配之类的一次性事件。只看首尾总量
// 会把这 +6 误判成泄漏(这正是本用例此前 flaky 的原因),所以改为:只有
// **多数轮次**都在增长才判泄漏。
func TestWindowsPTYNoHandleLeak(t *testing.T) {
	const (
		rounds = 6
		// 单轮容差:留一点运行时噪声的余量;真实泄漏是每轮 3 个以上的量级
		// (伪控制台 + 进程 + 管道句柄)。
		maxPerRoundGrowth = 2
	)

	// 先跑一轮预热:运行时 IOCP、惰性 DLL、Go 测试框架自己的一次性句柄
	// 都会在第一次会话时长出来,不能算到泄漏头上(实测预热后每轮增量为 0)。
	runTestShell(t)

	before := settledHandleCount(t)
	prev := before
	grownRounds := 0
	for i := 0; i < rounds; i++ {
		runTestShell(t)
		now := settledHandleCount(t)
		delta := int(now) - int(prev)
		t.Logf("handles after round %d: %d (delta %+d vs previous round, %+d vs baseline)",
			i, now, delta, int(now)-int(before))
		if delta > maxPerRoundGrowth {
			grownRounds++
		}
		prev = now
	}

	// Close 真漏则每轮都漏,多数轮次都会超容差;一次性跳变只影响一轮。
	if grownRounds*2 >= rounds {
		t.Fatalf("handle count grew in %d of %d rounds (%d → %d); Close leaks handles",
			grownRounds, rounds, before, prev)
	}
}

// TestWindowsPTYConPTY 覆盖一次最基本的真实 ConPTY 会话:启动 → 读到
// shell 输出 → Wait 得到 0 退出码 → 输出流收尾 → 重复 Close 也安全。
func TestWindowsPTYConPTY(t *testing.T) {
	p, out, readDone := startTestShell(t, os.Environ(), "/c", "echo "+conptySentinel)

	waitForExit(t, p)
	if !p.Exited() {
		t.Fatalf("Exited() = false after Wait() returned")
	}
	awaitReader(t, readDone)

	if got := stripANSI(out.String()); !strings.Contains(got, conptySentinel) {
		t.Fatalf("shell output does not contain %q:\nstripped: %q\nraw: %q", conptySentinel, got, out.String())
	}

	// Close 必须幂等:第一次释放句柄,第二次(以及 Tty.Close 之后的重复
	// 调用)不能二次关闭已经被系统释放、可能被复用的句柄。
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestWindowsPTYEnv 验证 cfg.env 真的被编码成了 UTF-16 环境块:格式错了
// CreateProcess 会直接失败,漏项则变量根本不存在。
func TestWindowsPTYEnv(t *testing.T) {
	const marker = "conpty-env-ok"
	env := append(os.Environ(), conptyEnvName+"="+marker)
	p, out, readDone := startTestShell(t, env, "/c", "echo %"+conptyEnvName+"%")

	waitForExit(t, p)
	awaitReader(t, readDone)
	if got := stripANSI(out.String()); !strings.Contains(got, marker) {
		t.Fatalf("shell output does not contain %q:\nstripped: %q\nraw: %q", marker, got, out.String())
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWindowsPTYResize 用活得够久的 shell 验证运行期 Resize 不报错
// (ConPTY 的尺寸只能靠 ResizePseudoConsole 改)。
func TestWindowsPTYResize(t *testing.T) {
	// ping 大概占用 3 秒,足够在 shell 存活期间改两次尺寸。
	p, _, readDone := startTestShell(t, os.Environ(), "/c", "ping -n 4 127.0.0.1 > nul")

	time.Sleep(500 * time.Millisecond)
	if p.Exited() {
		t.Fatalf("shell exited before Resize; ping 活得太短,无法验证运行期改尺寸")
	}
	if err := p.Resize(120, 40); err != nil {
		t.Fatalf("Resize(120, 40): %v", err)
	}
	if p.Exited() {
		t.Fatalf("shell exited during Resize")
	}
	// 第二次改尺寸同样要成功:浏览器每次 attach/拖动窗口都会重新下发。
	if err := p.Resize(120, 40); err != nil {
		t.Fatalf("second Resize(120, 40): %v", err)
	}
	// 非法尺寸必须被安静忽略,而不是把错误抛给会话管理器。
	if err := p.Resize(0, 0); err != nil {
		t.Fatalf("Resize(0, 0) = %v, want nil", err)
	}

	waitForExit(t, p)
	awaitReader(t, readDone)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWindowsPTYSignal 覆盖 Windows 上的信号语义:终止类信号退化成杀
// 进程(退出码 1),其余信号是 no-op,Kill/Signal 在退出后仍要安全。
func TestWindowsPTYSignal(t *testing.T) {
	p, _, readDone := startTestShell(t, os.Environ(), "/c", "ping -n 10 127.0.0.1 > nul")
	defer func() { _ = p.Close() }()

	// Windows 上没有对应语义的信号必须静默忽略,而且不能影响进程。
	if err := p.Signal(syscall.SIGALRM); err != nil {
		t.Fatalf("Signal(SIGALRM) = %v, want nil", err)
	}
	if p.Exited() {
		t.Fatalf("shell exited after a no-op signal")
	}
	// SIGTERM 在 Windows 上映射成 TerminateProcess,退出码固定为 1。
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal(SIGTERM) = %v", err)
	}
	err := waitForExitErr(t, p)
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("Wait() = %v, want an error mentioning the exit code 1", err)
	}
	// 进程已经退出:Kill 是 no-op,Signal 报 ErrExited(与 Unix 端一致)。
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill() after exit = %v, want nil", err)
	}
	if err := p.Signal(syscall.SIGTERM); !errors.Is(err, ErrExited) {
		t.Fatalf("Signal(SIGTERM) after exit = %v, want ErrExited", err)
	}
	awaitReader(t, readDone)
}
