# Mesh-Zero

![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Status](https://img.shields.io/badge/Status-Beta-orange)

**Mesh-Zero** is a CGO-free, zero-infrastructure, peer-to-peer WebAssembly (WASM) execution engine. 

It allows you to turn any group of devices—from MacBooks to Raspberry Pis—into a secure, globally routed serverless compute cluster. There are no centralized coordinators, no Docker containers, and no external infrastructure required. It compiles down to a single, static binary.

## Core Features

* **Zero Infrastructure:** Nodes automatically discover each other via local `mDNS` or the global Kademlia DHT. No central databases or API servers are needed.
* **Global NAT Traversal:** Built-in UPnP and AutoRelay allow nodes to securely connect across the public internet, even behind strict home routers and firewalls.
* **Sandboxed Execution:** Untrusted `.wasm` payloads are executed using [wazero](https://github.com/tetratelabs/wazero). The engine strictly enforces memory ceilings (6.4MB max) and execution timeouts (5-second fuel limit) natively in Go, preventing infinite loops and malicious payloads.
* **Idempotent Atomic Scheduling:** Uses a custom `MZ02` binary protocol with double-checked locking and thread-safe caching to ensure a payload is only ever executed once, even during mDNS discovery storms.
* **Embedded Control Plane:** Features a zero-dependency Visual Dashboard compiled directly into the binary using `//go:embed`.

---

## The `MZ02` Wire Protocol

To eliminate HTTP overhead, Mesh-Zero nodes communicate via multiplexed `libp2p` streams using a highly optimized 20-byte binary header.

| Field | Type | Size | Description |
| :--- | :--- | :--- | :--- |
| **Magic** | `[4]byte` | 4 Bytes | Protocol Identifier (`MZ02`). |
| **TaskID** | `uint64` | 8 Bytes | Unique ID to prevent double-execution across the mesh. |
| **WasmLen** | `uint32` | 4 Bytes | Byte size of the executable payload. |
| **ParamLen**| `uint32` | 4 Bytes | Byte size of the Stdin parameter data. |
| **Payload** | `[]byte` | Variable | The `.wasm` binary followed immediately by the input data. |

---

## Quickstart

### Prerequisites
* **Go** (v1.21+)
* **TinyGo** (For compiling WASI-compatible payloads)

### 1. Install
Clone the repository and build the binary:
```bash
git clone https://github.com/Rupamthxt/Mesh-Zero.git
cd mesh-zero
go build -o mesh-zero cmd/mesh-zero/main.go
```

### 2. Start a Worker Node
The Worker acts as the compute engine. It binds to all interfaces, advertises itself via DTH, and listens for WASM payloads.

```bash
./mesh-zero worker start 8080
```
(Open http://localhost:8080 in your browser to view the embedded Control Plane dashboard).
Output:
```bash
Worker Node 12D3KooW... listening. Waiting for tasks...
```
### 3. Compile a WASM task
Write a generic task in Go. Mesh-Zero uses `os.Stdin` to inject parameters and `os.Stdout` to stream results.
```bash
# hasher.go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func main() {
	input, _ := io.ReadAll(os.Stdin)
	hash := sha256.Sum256(input)
	fmt.Printf("[WASM] SHA-256: %s\n", hex.EncodeToString(hash[:]))
}
```
Compile it to a lightweight WASI binary:
```bash
tinygo build -o hasher.wasm -target=wasi hasher.go
```
### 4. Submit the task via the sender
The Sender acts as a load balancer. It discovers available Workers, atomically locks a stream, and blasts the payload.

Create a data file to process:
```bash
echo "Hello from the decentralized edge!" > input.txt
```
Send it to the mesh:
```bash
./mesh-zero run hasher.wasm input.txt
```
The CLI will ping your local daemon, locate a remote peer on the global DHT, transmit the payload, and stream the standard output back to your terminal in milliseconds.

## Contributing
Contributions are welcome! Currently looking for help with:
* Implementing an explicit "Fuel" metric (instruction counting) instead of just time limits.
* Creating an SDK for Rust and AssemblyScript guest payloads.