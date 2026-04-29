package core

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

//go:embed dashboard.html
var dashboardHTML []byte

func (w *Worker) StartAPIServer(port string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/peers", w.handleGetPeers)
	mux.HandleFunc("/api/execute", w.handleExecuteTask)

	mux.HandleFunc("/", func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Content-Type", "text/html")
		res.Write(dashboardHTML)
	})

	handler := corsMiddleware(mux)

	fmt.Printf("[API] Local Gateway listening on http://localhost:%s\n", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		fmt.Printf("[API] Fatal server error: %v\n", err)
	}
}

func (w *Worker) handleGetPeers(res http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(res, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	peerList := w.Host.Network().Peers()

	targetRendezvous := "mesh-zero-local-v1"

	var peerIDs []string
	for _, p := range peerList {
		val, err := w.Host.Peerstore().Get(p, "rendezvous")
		if err == nil && val == targetRendezvous {
			peerIDs = append(peerIDs, p.String())
		}
	}

	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(map[string]interface{}{
		"node_id": w.Host.ID().String(),
		"peers":   peerIDs,
		"count":   len(peerIDs),
	})
}

func (w *Worker) handleExecuteTask(res http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(res, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req.ParseMultipartForm(10 << 20)

	wasmFile, _, err := req.FormFile("wasm")
	if err != nil {
		http.Error(res, "Missing 'wasm' file", http.StatusBadRequest)
		return
	}
	defer wasmFile.Close()
	wasmBytes, _ := io.ReadAll(wasmFile)

	dataFile, _, err := req.FormFile("data")
	if err != nil {
		http.Error(res, "Missing 'data' file", http.StatusBadRequest)
		return
	}
	defer dataFile.Close()
	dataBytes, _ := io.ReadAll(dataFile)

	targetPeerID := req.FormValue("peer_id")
	if targetPeerID == "" {
		http.Error(res, "Missing target 'peer_id'", http.StatusBadRequest)
		return
	}

	peers := w.Host.Network().Peers()
	var selectedPeer *peer.ID
	for _, p := range peers {
		if p.String() == targetPeerID {
			selectedPeer = &p
			break
		}
	}

	if selectedPeer == nil {
		http.Error(res, "Target peer not connected", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := w.Host.NewStream(ctx, *selectedPeer, "/mesh-zero/task/1.0.0")
	if err != nil {
		http.Error(res, "Failed to open mesh stream", http.StatusInternalServerError)
		return
	}
	defer s.Close()

	taskId := uint64(time.Now().UnixNano())

	privHex := os.Getenv("MESH_PRIV_KEY")
	if privHex == "" {
		fmt.Println("[SECURITY] Missing MESH_PRIV_KEY. Cannot sign payload.")
		return
	}
	privKey, _ := hex.DecodeString(privHex)

	signData := make([]byte, 16)
	binary.BigEndian.PutUint64(signData[0:8], taskId)
	binary.BigEndian.PutUint32(signData[8:12], uint32(len(wasmBytes)))
	binary.BigEndian.PutUint32(signData[12:16], uint32(len(dataBytes)))

	signature := ed25519.Sign(ed25519.PrivateKey(privKey), signData)

	header := make([]byte, 84)
	copy(header[:4], "MZ03")
	binary.BigEndian.PutUint64(header[4:12], taskId)
	binary.BigEndian.PutUint32(header[12:16], uint32(len(wasmBytes)))
	binary.BigEndian.PutUint32(header[16:20], uint32(len(dataBytes)))
	copy(header[20:84], signature)

	s.Write(header)
	s.Write(wasmBytes)
	s.Write(dataBytes)

	resultBytes, _ := io.ReadAll(s)

	res.Header().Set("Content-Type", "text/plain")
	res.Write(resultBytes)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
