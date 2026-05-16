// Nakama Ping/Pong client example.
//
// Demonstrates both layers of the Nakama heartbeat system:
//
//   Layer 1 (automatic) — WebSocket protocol ping/pong frames.
//     The server sends WS ping frames every PingPeriodMs (default 15s).
//     The Go websocket library responds automatically with pong frames.
//     No pong within PongWaitMs (default 25s) closes the connection.
//
//   Layer 2 (application) — Nakama envelope Ping/Pong.
//     The client sends an Envelope_Ping, the server replies with Envelope_Pong.
//     This lets the client measure round-trip time at the application layer.
//
// Usage:
//
//	go run ./examples/ping-pong/
//
// Assumes Nakama is running locally on port 7350 with default keys.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/heroiclabs/nakama-common/rtapi"
)

const (
	nakamaHost   = "http://localhost:7350"
	nakamaWS     = "ws://localhost:7350"
	serverKey    = "defaultkey"
	pingInterval = 5 * time.Second
)

func main() {
	deviceID := genDeviceID()
	log.Printf("Device ID: %s", deviceID)

	// Step 1: Authenticate and get a session token.
	token, err := authenticate(deviceID)
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}
	log.Printf("Token obtained: %s...", token[:40])

	// Step 2: Connect via WebSocket (protobuf format for envelope messages).
	conn, err := connectWS(token)
	if err != nil {
		log.Fatalf("WebSocket connection failed: %v", err)
	}
	defer conn.Close()
	log.Println("WebSocket connected (protobuf format)")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Pending pings keyed by correlation ID.
	var mu sync.Mutex
	pending := make(map[string]chan time.Duration)

	// Step 3: Start application-level ping sender.
	go pingSender(ctx, conn, pending, &mu)

	// Step 4: Read loop — handles all incoming messages.
	readLoop(ctx, conn, pending, &mu)
}

func authenticate(deviceID string) (string, error) {
	// The gRPC gateway maps the HTTP body to the "account" field only,
	// so we send just the AccountDevice JSON, not the full request wrapper.
	payload, err := json.Marshal(map[string]string{"id": deviceID})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", nakamaHost+"/v2/account/authenticate/device", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(serverKey, "")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return "", fmt.Errorf("unexpected status: %s, body: %s", resp.Status, body.String())
	}

	var session struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("decode session: %w", err)
	}
	return session.Token, nil
}

func connectWS(token string) (*websocket.Conn, error) {
	url := fmt.Sprintf("%s/ws?lang=en&status=true&format=protobuf&token=%s", nakamaWS, token)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	return conn, nil
}

// pingSender periodically sends Envelope_Ping messages.
func pingSender(ctx context.Context, conn *websocket.Conn, pending map[string]chan time.Duration, mu *sync.Mutex) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	seq := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq++
			cid := fmt.Sprintf("ping-%d", seq)
			ch := make(chan time.Duration, 1)

			mu.Lock()
			pending[cid] = ch
			mu.Unlock()

			env := &rtapi.Envelope{
				Cid:     cid,
				Message: &rtapi.Envelope_Ping{Ping: &rtapi.Ping{}},
			}
			data, err := proto.Marshal(env)
			if err != nil {
				log.Printf("Marshal ping error: %v", err)
				return
			}

			start := time.Now()
			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				log.Printf("Write ping error: %v", err)
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				log.Printf("seq=%d timeout waiting for pong", seq)
			case rtt := <-ch:
				_ = rtt
				log.Printf("seq=%d RTT=%v (sent at %s)", seq, time.Since(start), start.Format("15:04:05.000"))
			}

			mu.Lock()
			delete(pending, cid)
			mu.Unlock()
		}
	}
}

// readLoop reads messages from the WebSocket and dispatches them.
func readLoop(ctx context.Context, conn *websocket.Conn, pending map[string]chan time.Duration, mu *sync.Mutex) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("Read error: %v", err)
			return
		}

		var env rtapi.Envelope
		if err := proto.Unmarshal(raw, &env); err != nil {
			log.Printf("Unmarshal error: %v", err)
			continue
		}

		switch env.Message.(type) {
		case *rtapi.Envelope_Pong:
			// Deliver to the pending ping if there's a matching cid.
			if env.Cid != "" {
				mu.Lock()
				ch, ok := pending[env.Cid]
				mu.Unlock()
				if ok {
					select {
					case ch <- 0:
					default:
					}
				}
			}
		case *rtapi.Envelope_StatusPresenceEvent:
			// Initial status presence — received after connect.
			log.Printf("Status presence received (initial sync)")
		default:
			// Silence other messages.
		}
	}
}

func genDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
