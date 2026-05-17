// Nakama Tournament + Leaderboard client example.
//
// One client creates a 5-minute tournament via RPC.
// 10 clients join, submit scores, and periodically bump them.
// A display loop polls the tournament leaderboard every second.
//
// Prerequisites:
//
//  1. Place data/modules/tournament.lua in your Nakama runtime path
//     (data/modules/ by default). It registers the
//     clientrpc.create_tournament RPC used to create the tournament.
//  2. Start the Nakama server.
//
// Usage:
//
//	go run ./examples/tournament/
//
// Assumes Nakama is running locally on port 7350 with default keys.
// No manual player registration needed — device authentication creates
// users automatically.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const (
	nakamaHost    = "http://localhost:7350"
	serverKey     = "defaultkey"
	numClients    = 10
	pollInterval  = 1 * time.Second
	tournamentDur = 5 * time.Minute
)

// rpcResponse is the outer envelope returned by Nakama's /v2/rpc endpoint.
type rpcResponse struct {
	Id      string `json:"id"`
	Payload string `json:"payload"`
}

// tournamentCreateResponse is the JSON inside rpcResponse.Payload.
type tournamentCreateResponse struct {
	TournamentID string `json:"tournament_id"`
	Title        string `json:"title"`
}

type leaderboardRecord struct {
	LeaderboardID string `json:"leaderboard_id"`
	OwnerID       string `json:"owner_id"`
	Username      string `json:"username"`
	Score         int64  `json:"score,string"`
	Subscore      int64  `json:"subscore,string"`
	NumScore      int32  `json:"num_score"`
	Rank          int64  `json:"rank,string"`
}

type tournamentRecordList struct {
	Records      []leaderboardRecord `json:"records"`
	OwnerRecords []leaderboardRecord `json:"owner_records"`
	NextCursor   string              `json:"next_cursor"`
	PrevCursor   string              `json:"prev_cursor"`
}

func main() {
	log.SetFlags(0)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	suffix := randSuffix(4)

	// --- Step 1: Create the tournament via client RPC ---
	creatorToken, err := authenticate(genDeviceID())
	if err != nil {
		log.Fatalf("Creator auth failed: %v", err)
	}

	tournamentID, err := createTournament(creatorToken)
	if err != nil {
		log.Fatalf("Create tournament failed: %v", err)
	}
	log.Printf("Tournament created: %s (duration: %v)", tournamentID, tournamentDur)

	// --- Step 2: Start all player clients concurrently ---
	done := make(chan struct{}, numClients)
	for i := range numClients {
		go func(idx int) {
			runClient(ctx, idx, suffix, tournamentID)
			done <- struct{}{}
		}(i)
	}

	// --- Step 3: Display loop ---
	displayToken, err := authenticate(genDeviceID())
	if err != nil {
		log.Fatalf("Display auth failed: %v", err)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	startTime := time.Now()

	fmt.Print("\033[H\033[2J") // clear screen once
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(startTime)
			list := fetchTournamentRecords(displayToken, tournamentID)
			if list != nil {
				printLeaderboard(list, elapsed)
			}
		}
	}
}

// --- Client lifecycle ---

func runClient(ctx context.Context, idx int, suffix, tournamentID string) {
	deviceID := genDeviceID()
	username := fmt.Sprintf("p%d-%s", idx, suffix)

	token, err := authenticate(deviceID)
	if err != nil {
		log.Printf("[%s] Auth failed: %v", username, err)
		return
	}

	if err := updateAccount(token, username); err != nil {
		log.Printf("[%s] Username update failed (continuing anyway): %v", username, err)
	}

	if err := joinTournament(token, tournamentID); err != nil {
		log.Printf("[%s] Join tournament failed: %v", username, err)
		return
	}
	log.Printf("[%s] Joined tournament", username)

	score, _ := rand.Int(rand.Reader, big.NewInt(5001))
	scoreVal := score.Int64()
	if err := submitTournamentScore(token, tournamentID, scoreVal); err != nil {
		log.Printf("[%s] Initial score submit failed: %v", username, err)
		return
	}
	log.Printf("[%s] Initial score: %d", username, scoreVal)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// ~20% chance each second to bump score by 0–200.
			if randOneIn(5) {
				delta, _ := rand.Int(rand.Reader, big.NewInt(201))
				scoreVal += delta.Int64()
				if err := submitTournamentScore(token, tournamentID, scoreVal); err != nil {
					log.Printf("[%s] Score update failed: %v", username, err)
				}
			}
		}
	}
}

// --- API helpers ---

func authenticate(deviceID string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"id": deviceID})
	req, _ := http.NewRequest("POST", nakamaHost+"/v2/account/authenticate/device", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(serverKey, "")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body.String())
	}

	var session struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", err
	}
	return session.Token, nil
}

func updateAccount(token, username string) error {
	payload, _ := json.Marshal(map[string]string{"username": username})
	req, _ := http.NewRequest("PUT", nakamaHost+"/v2/account", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, body.String())
	}
	return nil
}

func createTournament(token string) (string, error) {
	argsJSON, _ := json.Marshal(map[string]any{
		"authoritative":  false,
		"sort_order":     "desc",
		"operator":       "best",
		"duration":       300,
		"reset_schedule": nil,
		"title":          "5-Minute Tournament",
		"description":    "A 5-minute tournament with 10 players",
		"category":       0,
		"start_time":     0,
		"end_time":       0,
		"max_size":       0,
		"max_num_score":  0,
		"join_required":  true,
	})
	// The RPC endpoint body is bound directly to the string "payload" field,
	// so the body must be a JSON string, not a JSON object.
	payload, _ := json.Marshal(string(argsJSON))

	url := fmt.Sprintf("%s/v2/rpc/clientrpc.create_tournament", nakamaHost)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("RPC call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body.String())
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", fmt.Errorf("decode RPC response: %w", err)
	}

	var createResp tournamentCreateResponse
	if err := json.Unmarshal([]byte(rpcResp.Payload), &createResp); err != nil {
		return "", fmt.Errorf("decode tournament create response: %w", err)
	}

	return createResp.TournamentID, nil
}

func joinTournament(token, tournamentID string) error {
	url := fmt.Sprintf("%s/v2/tournament/%s/join", nakamaHost, tournamentID)
	req, _ := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, body.String())
	}
	return nil
}

func submitTournamentScore(token, tournamentID string, score int64) error {
	// gRPC-Gateway protojson accepts both number and string for int64,
	// but string is the canonical wire format.
	payload, _ := json.Marshal(map[string]any{
		"score":    fmt.Sprintf("%d", score),
		"subscore": "0",
	})
	url := fmt.Sprintf("%s/v2/tournament/%s", nakamaHost, tournamentID)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, body.String())
	}
	return nil
}

func fetchTournamentRecords(token, tournamentID string) *tournamentRecordList {
	url := fmt.Sprintf("%s/v2/tournament/%s?limit=10", nakamaHost, tournamentID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		log.Printf("Fetch tournament failed: status %d: %s", resp.StatusCode, body.String())
		return nil
	}

	var list tournamentRecordList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Printf("Decode tournament failed: %v", err)
		return nil
	}
	return &list
}

// --- Display ---

func printLeaderboard(list *tournamentRecordList, elapsed time.Duration) {
	// Move cursor to home without clearing (avoids flicker).
	fmt.Print("\033[H\033[2K")
	fmt.Printf("=== TOURNAMENT LEADERBOARD ===  elapsed: %v  (Ctrl+C to quit)", elapsed.Round(time.Second))
	fmt.Print("\033[K")
	fmt.Println()
	if elapsed >= tournamentDur {
		fmt.Print("\033[K")
		fmt.Println("*** TOURNAMENT HAS ENDED — final standings below ***")
	}
	fmt.Println()
	fmt.Printf("%-6s %-14s %10s\n", "RANK", "PLAYER", "SCORE")
	fmt.Print("\033[K")
	fmt.Println("------ ------------  ----------")

	for _, r := range list.Records {
		fmt.Print("\033[K")
		fmt.Printf("%-6d %-14s %10d\n", r.Rank, r.Username, r.Score)
	}

	// Clear rest of screen.
	fmt.Print("\033[J")
}

// --- Utilities ---

func genDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}

func randOneIn(n int) bool {
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return v.Int64() == 0
}
