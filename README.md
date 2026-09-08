# larkdesk

macOS CLI that reuses the already-logged-in **Lark.app / 飞书** desktop session. No OAuth, no `lark-cli`.

Requires: macOS, Python 3.10+, 飞书已登录。

## Install

```bash
pipx install larkdesk
```

or:

```bash
uv tool install larkdesk
```

or:

```bash
pip install larkdesk
```

From a clone:

```bash
chmod +x install.sh
./install.sh
```

Missing desktop login is a non-zero error.
