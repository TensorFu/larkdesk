# larkdesk

macOS CLI that reuses the already-logged-in **Lark.app / 飞书** desktop session. No OAuth, no `lark-cli`.

Single static binary. Requires: macOS, 飞书已登录。

## Install

From a clone (needs Go 1.24+):

```bash
./install.sh
```

or:

```bash
go install github.com/TensorFu/larkdesk/cmd/larkdesk@latest
```

Missing desktop login is a non-zero error.
