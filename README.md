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

Missing desktop login is a non-zero error.

