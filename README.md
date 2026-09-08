# larkdesk

macOS CLI that reuses the already-logged-in **Lark.app / 飞书** desktop session. No OAuth, no `lark-cli`.

Requires: macOS, Python 3.10+, 飞书已登录。

## One-click install

From a clone of this repo:

```bash
chmod +x install.sh
./install.sh
```

After it is on GitHub:

```bash
pipx install git+https://github.com/TensorFu/larkdesk.git
```

or:

```bash
uv tool install git+https://github.com/TensorFu/larkdesk.git
```

## Commands

```bash
larkdesk whoami
larkdesk contacts
larkdesk search 钟林
larkdesk send 钟林 hello --dry-run
larkdesk send 钟林 hello
```

`send` only needs a person (name or id) and text. The CLI searches, get-or-creates the P2P chat, then sends.

Missing desktop login is a non-zero error.

