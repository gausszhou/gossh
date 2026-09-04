#!/usr/bin/env bash
# gossh 端到端冒烟:对本地 sshd 做真实 SSH 连接验证。
# 用法:scripts/smoke.sh [serve-binary] [port]
# 需要:本地 sshd 运行;可选 GOSSH_TEST_KEY=~/.ssh/xxx 指定测试密钥
# (默认生成临时密钥并写入 ~/.ssh/authorized_keys)。
set -euo pipefail

BIN="${1:-./build/gossh}"
PORT="${2:-18040}"
DIR="$(mktemp -d)"
TOKEN="smoketoken"
HOSTID=""
SESSION="aaaaaaaaaaaaaaaa"

cleanup() {
  kill "$SERVER_PID" 2>/dev/null || true
  if [[ -n "${KEY_RM:-}" ]]; then sed -i "\|$KEY_RM|d" ~/.ssh/authorized_keys 2>/dev/null || true; fi
  rm -rf "$DIR"
}
trap cleanup EXIT

KEY="$DIR/id_ed25519"
ssh-keygen -t ed25519 -f "$KEY" -N "" -q -C "gossh-smoke"
PUB=$(cat "$KEY.pub")
grep -qF "$PUB" ~/.ssh/authorized_keys 2>/dev/null || echo "$PUB" >> ~/.ssh/authorized_keys
KEY_RM="$PUB"

"$BIN" serve --port "$PORT" --token "$TOKEN" \
  --hosts-file "$DIR/hosts.json" --known-hosts-file "$DIR/known_hosts" \
  --session-file "$DIR/sessions.json" --log-file "" >"$DIR/serve.log" 2>&1 &
SERVER_PID=$!
for i in $(seq 1 20); do curl -sf -o /dev/null "http://127.0.0.1:$PORT/api/hosts?token=$TOKEN" && break; sleep 0.3; done

API="http://127.0.0.1:$PORT"
H=(-H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json")

echo "== 1. token gate =="
[ "$(curl -s -o /dev/null -w '%{http_code}' "$API/api/hosts")" = "401" ]

echo "== 2. add host =="
HOSTID=$(curl -s -X POST "$API/api/hosts" "${H[@]}" \
  -d "{\"name\":\"local\",\"address\":\"127.0.0.1\",\"port\":22,\"user\":\"$USER\",\"credential\":{\"kind\":\"key\",\"key_path\":\"$KEY\"}}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')

echo "== 3. run command (TOFU pin + exit code) =="
OUT=$(curl -s -X POST "$API/api/run" "${H[@]}" \
  -d "{\"host_id\":\"$HOSTID\",\"command\":\"echo SMOKE_OK && exit 7\"}")
echo "$OUT" | grep -q SMOKE_OK
echo "$OUT" | python3 -c 'import json,sys; assert json.load(sys.stdin)["exit_code"]==7'

echo "== 4. interactive session + screen mirror =="
curl -s -X POST "$API/api/sessions" "${H[@]}" \
  -d "{\"host_id\":\"$HOSTID\",\"id\":\"$SESSION\"}" | python3 -c 'import json,sys; assert json.load(sys.stdin)["id"]=="aaaaaaaaaaaaaaaa"'
sleep 0.5
curl -s "$API/api/sessions/$SESSION/screen?format=text" "${H[@]}" | grep -q .   # 非空(MOTD/prompt)

echo "== 5. SFTP over the session =="
curl -s "$API/api/sessions/$SESSION/sftp/ls?path=/tmp" "${H[@]}" | python3 -c 'import json,sys; assert isinstance(json.load(sys.stdin), list)'

echo "== 6. local port forward =="
FID=$(curl -s -X POST "$API/api/sessions/$SESSION/forwards" "${H[@]}" \
  -d '{"kind":"local","bind":"127.0.0.1:18099","target":"localhost:22"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
BANNER=$(timeout 2 bash -c 'exec 3<>/dev/tcp/127.0.0.1/18099; head -c 20 <&3' 2>/dev/null || true)
[[ "$BANNER" == SSH-2.0-* ]]

echo "== 7. host-level forward auto-apply =="
# 主机记录附持久转发;同 id 幂等命中已有会话不会重放,故用独立会话 id 验证
curl -s -X PUT "$API/api/hosts/$HOSTID" "${H[@]}" \
  -d "{\"id\":\"$HOSTID\",\"name\":\"local\",\"address\":\"127.0.0.1\",\"port\":22,\"user\":\"$USER\",\"credential\":{\"kind\":\"key\",\"key_path\":\"$KEY\"},\"forwards\":[{\"kind\":\"local\",\"bind\":\"127.0.0.1:18100\",\"target\":\"localhost:22\"}]}" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert len(d.get("forwards",[]))==1'
FSID="bbbbbbbbbbbbbbbb"
curl -s -X POST "$API/api/sessions" "${H[@]}" \
  -d "{\"host_id\":\"$HOSTID\",\"id\":\"$FSID\"}" | python3 -c 'import json,sys; assert json.load(sys.stdin)["id"]=="bbbbbbbbbbbbbbbb"'
sleep 0.3
curl -s "$API/api/sessions/$FSID/forwards" "${H[@]}" \
  | python3 -c 'import json,sys; fs=json.load(sys.stdin); assert any(f["bind"]=="127.0.0.1:18100" for f in fs), fs'
HBANNER=$(timeout 2 bash -c 'exec 3<>/dev/tcp/127.0.0.1/18100; head -c 20 <&3' 2>/dev/null || true)
[[ "$HBANNER" == SSH-2.0-* ]]
curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/api/sessions/$FSID" "${H[@]}" | grep -q 204

echo "== 8. keys injection =="
curl -s -X POST "$API/api/sessions/$SESSION/keys" "${H[@]}" -d '{"input":"echo INJECTED\r"}' | grep -q '"written"'
sleep 0.6
curl -s "$API/api/sessions/$SESSION/screen?format=text" "${H[@]}" | grep -q INJECTED

echo "== 9. lifecycle: destroy + resurrect =="
curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/api/sessions/$SESSION" "${H[@]}" | grep -q 204
curl -s -X POST "$API/api/sessions" "${H[@]}" -d "{\"id\":\"$SESSION\"}" | grep -q '"state":"idle"'

echo "== 10. known-hosts manage =="
curl -s "$API/api/known-hosts" "${H[@]}" | grep -q "127.0.0.1:22"

echo "== 11. clean up session =="
curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/api/sessions/$SESSION" "${H[@]}" | grep -q 204

echo
echo "ALL SMOKE CHECKS PASSED"