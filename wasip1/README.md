# gh for WebAssembly (wasip1)

This branch builds `gh` as a `wasm32-wasip1` command, so it can run as a process
inside [BrowserOS](https://github.com/Progate/packages) - a browser tab, with no
server and no native binary.

```sh
./wasip1/build.sh gh.wasm
```

Everything below is what that costs, so that the next person does not have to
find it out again.

## The network

WASI preview1 has no `connect(2)`. Its socket calls (`sock_accept`, `sock_recv`,
`sock_send`) assume the host handed the guest a socket, so Go's wasip1 port has
no dialer at all: `net.Dial` returns "not implemented". A `gh` built this way
would not be able to reach anything.

BrowserOS closes that hole without extending the ABI - it exposes its socket
table as a path, using the name bash uses:

```
/dev/tcp/<host>/<port>      opening it yields a connected socket
```

`internal/browseros` dials by opening that path, marks the fd non-blocking so
that only the reading goroutine parks (a blocking `fd_read` would stop the whole
module - wasip1 has no threads), and hands the fd to `net/http` as a `net.Conn`.
`Main` installs it as `http.DefaultTransport`, which is what go-gh builds its
client on.

**TLS is the host's job.** A browser cannot open a raw TCP connection, so
BrowserOS reaches the outside through a gateway that speaks `fetch()`, and that
gateway is what terminates TLS: it treats port 443 as https. So the guest writes
plain HTTP/1.1 to port 443 and lets the host wrap it. Wrapping it here instead
would hand the gateway bytes it cannot read - and wasip1 has no root
certificates to verify with anyway.

## Hosts may carry a port

`gh --hostname localhost:3000` is accepted on wasip1 (`internal/ghinstance`).
The interesting server in a browser is usually one the user just started next
door, on whatever port it was given, and without a port there would be no way to
name it. A host with a port is not `github.com`, so the rest of gh already treats
it as GitHub Enterprise and asks `/api/v3/` and `/api/graphql`.

## Prompts are line-based

The three prompters gh picks between all drive a terminal directly: raw mode,
cursor movement, resize signals. wasip1 has no ioctl, so none of them can work.
`internal/prompter` has a wasip1 twin that prints the question and reads a line;
selections are made by number, `MarkdownEditor` reads until a line with a single
dot, and passwords echo (the guest cannot turn echo off, so the prompt says so).

## What is left out, and why

| Left out | Reason |
| --- | --- |
| `gh codespace` | terminal UI (tcell) and an SSH tunnel |
| `gh attestation` | sigstore's trust root, and tens of megabytes |
| `gh ext browse` | full-screen tview UI. `gh ext search` still works |
| anything that runs `git` or an editor | wasip1 has no `fork`/`exec` |

Dropping the first three is also what keeps tcell, dev-tunnels, bubbletea and
survey out of the module, so those never have to be taught about wasip1.

## Dependency patches

Three dependencies do not build for wasip1. They are vendored at build time and
patched (`wasip1/patches`), not forked, because each fix is a build tag or a
stub for a syscall that does not exist:

| Module | Patch |
| --- | --- |
| `github.com/muesli/termenv` | add `wasip1` to the tags of the no-terminal file |
| `github.com/atotto/clipboard` | a stub: the browser owns the clipboard |
| `github.com/in-toto/in-toto-golang` | `unix.Access` has no wasip1 equivalent; ask the filesystem instead |

`wasip1/build.sh` re-vendors before applying them, so a patch that stops applying
means an upgrade changed something worth looking at.

## Keeping the fork small

Everything is behind `//go:build wasip1`, and the twin file for other platforms
sits next to it. `go build ./...` on a normal platform must keep working; that is
the one rule this branch has.
