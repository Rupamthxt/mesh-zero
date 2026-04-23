package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/rupamthxt/mesh-zero/sender"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type discoveryNotifee struct{}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	fmt.Printf("[mDNS] Worker saw peer on network: %s\n", pi.ID)
}

var completedTasks = make(map[uint64]bool)
var taskMu sync.Mutex

func main() {

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "worker":
		handleWorkerCommand()
	case "run":
		if len(os.Args) < 4 {
			fmt.Println("Usage: mesh-zero run <task.wasm> <input.data>")
			os.Exit(1)
		}
		wasmPath := os.Args[2]
		inputPath := os.Args[3]
		fmt.Printf("Submitting %s to the mesh...\n", wasmPath)
		sender.RunSender(wasmPath, inputPath)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
	}

}

func printUsage() {
	fmt.Printf(`Mesh-Zero: Decentralized Compute Node
		Usage:
		mesh-zero worker start    - Run the node in the foreground
		mesh-zero worker daemon   - Run the node silently in the background
		mesh-zero run <file>      - Submit a WASM task to the mesh
		\n`)
}

func handleWorkerCommand() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: mesh-zero worker [start|daemon]")
		os.Exit(1)
	}

	subCommand := os.Args[2]
	if subCommand == "start" {
		fmt.Println("Starting Mesh-Zero Node in foreground...")
		runWorkerNode()
		return
	}

	if subCommand == "daemon" {
		fmt.Println("Spawning Mesh-Zero daemon...")

		binaryPath, _ := os.Executable()
		cmd := exec.Command(binaryPath, "worker", "start")

		logFile, err := os.OpenFile("mesh-zero.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Printf("Failed to open log file: %v\n", err)
			return
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		err = cmd.Start()
		if err != nil {
			fmt.Printf("Failed to start daemon: %v\n", err)
			return
		}

		fmt.Printf("Daemon running in background. (PID: %d)\n", cmd.Process.Pid)
		fmt.Println("Check mesh-zero.log for node output.")
		os.Exit(0)
	}
}

func runWorkerNode() {
	ctx := context.Background()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
	if err != nil {
		fmt.Printf("Fatal host error: %v\n", err)
		return
	}
	defer h.Close()

	h.SetStreamHandler("/mesh-zero/task/1.0.0", func(s network.Stream) {
		handleTaskStream(ctx, s)
	})

	rendezvous := "mesh-zero-local-v1"
	mdnsService := mdns.NewMdnsService(h, rendezvous, &discoveryNotifee{})
	if err := mdnsService.Start(); err != nil {
		fmt.Printf("Fatal mDNS error: %v\n", err)
		return
	}

	fmt.Printf("Worker Node %s listening on Localhost. Waiting for tasks...\n", h.ID())
	select {}
}

func handleTaskStream(ctx context.Context, s network.Stream) {
	defer s.Close()

	header := make([]byte, 20)
	if _, err := io.ReadFull(s, header); err != nil {
		return
	}
	if string(header[:4]) != "MZ02" {
		return
	}

	taskID := binary.BigEndian.Uint64(header[4:12])
	wasmLen := binary.BigEndian.Uint32(header[12:16])
	paramLen := binary.BigEndian.Uint32(header[16:20])

	taskMu.Lock()
	if completedTasks[taskID] {
		fmt.Printf("Worker already executed Task %d. Ignoring.\n", taskID)
		taskMu.Unlock()
		return
	}
	completedTasks[taskID] = true
	taskMu.Unlock()

	wasmBin := make([]byte, wasmLen)
	io.ReadFull(s, wasmBin)

	paramBin := make([]byte, paramLen)
	io.ReadFull(s, paramBin)

	fmt.Printf("\n[WORKER] Task Received! WASM: %dB, Params: %dB\n", wasmLen, paramLen)
	runWasmTask(ctx, wasmBin, paramBin, s)
}

func runWasmTask(ctx context.Context, wasmBytes []byte, params []byte, out io.Writer) {
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	_, _ = r.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, vectorID uint32, ptr uint32) {
			fmt.Printf("[HOST] WASM requested Vector ID: %d. Injecting data from simulated mmap...\n", vectorID)
			simulatedVector := []byte(`[0.12, 0.88, 0.45]`)
			mod.Memory().Write(ptr, simulatedVector)
		}).
		Export("fetch_vector").
		Instantiate(ctx)

	compiledMod, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		fmt.Fprintf(out, "Compilation error: %v\n", err)
		return
	}

	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(params)).
		WithStdout(out).
		WithStderr(os.Stderr)

	_, err = r.InstantiateModule(ctx, compiledMod, config)
	if err != nil {
		fmt.Fprintf(out, "Execution error: %v\n", err)
	}
}
