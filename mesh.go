package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type discoveryNotifee struct{}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	fmt.Printf("[mDNS] Worker saw peer on network: %s\n", pi.ID)
}

func main() {
	ctx := context.Background()

	// 1. Lock to IPv4 localhost to bypass firewall roulette
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		fmt.Printf("Fatal host error: %v\n", err)
		return
	}
	defer h.Close()

	// 2. Set up the Stream Handler
	h.SetStreamHandler("/mesh-zero/task/1.0.0", func(s network.Stream) {
		handleTaskStream(ctx, s)
	})

	// 3. Start mDNS Discovery
	rendezvous := "mesh-zero-local-v1"
	mdnsService := mdns.NewMdnsService(h, rendezvous, &discoveryNotifee{})
	if err := mdnsService.Start(); err != nil {
		fmt.Printf("Fatal mDNS error: %v\n", err)
		return
	}

	fmt.Printf("Worker Node %s listening on Localhost. Waiting for tasks...\n", h.ID())
	select {} // Block forever
}

func handleTaskStream(ctx context.Context, s network.Stream) {
	defer s.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(s, header); err != nil {
		return
	}
	if string(header[:4]) != "MZ01" {
		return
	}

	wasmLen := binary.BigEndian.Uint32(header[4:8])
	paramLen := binary.BigEndian.Uint32(header[8:12])

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

	// --- THE MEMORY ENGINE HOOK (Path B) ---
	_, _ = r.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, vectorID uint32, ptr uint32) {
			fmt.Printf("[HOST] WASM requested Vector ID: %d. Injecting data from simulated mmap...\n", vectorID)
			simulatedVector := []byte(`[0.12, 0.88, 0.45]`)
			mod.Memory().Write(ptr, simulatedVector)
		}).
		Export("fetch_vector").
		Instantiate(ctx)
	// ---------------------------------------

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
