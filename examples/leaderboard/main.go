// Nakama Leaderboard client example.
//
// Spawns 10 clients that submit scores and periodically bump them.
// A separate display loop polls the leaderboard every second and
// prints the top-10 rankings.
//
// Prerequisites:
//
//  1. Place data/modules/leaderboard.lua in your Nakama runtime path
//     (data/modules/ by default). It auto-creates the "global" leaderboard
//     on server startup via nk.run_once.
//  2. Start the Nakama server — the leaderboard will be created on boot.
//
// Usage:
//
//	go run ./examples/leaderboard/
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
	leaderboardID = "global"
	numClients    = 10
	pollInterval  = 1 * time.Second
)

type leaderboardRecord struct {
	LeaderboardID string `json:"leaderboard_id"`
	OwnerID       string `json:"owner_id"`
	Username      string `json:"username"`
	Score         int64  `json:"score,string"`
	Subscore      int64  `json:"subscore,string"`
	NumScore      int32  `json:"num_score"`
	Rank          int64  `json:"rank,string"`
}

type leaderboardRecordList struct {
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

	// Shared auth token for the display loop.
	displayToken, err := authenticate(genDeviceID())
	if err != nil {
		log.Fatalf("Display auth failed: %v", err)
	}

	// Start all player clients concurrently.
	done := make(chan struct{}, numClients)
	for i := range numClients {
		go func(idx int) {
			runClient(ctx, idx, suffix)
			done <- struct{}{}
		}(i)
	}

	// Display loop — polls leaderboard every second.
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	fmt.Print("\033[H\033[2J") // clear screen once
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			list := fetchLeaderboard(displayToken)
			if list != nil {
				printLeaderboard(list)
			}
		}
	}
}

func runClient(ctx context.Context, idx int, suffix string) {
	deviceID := genDeviceID()
	username := fmt.Sprintf("p%d-%s", idx, suffix)

	token, err := authenticate(deviceID)
	if err != nil {
		log.Printf("[%s] Auth failed: %v", username, err)
		return
	}

	if err := updateAccount(token, username); err != nil {
		// username might be taken from a previous run; try without update.
		log.Printf("[%s] Username update failed (continuing anyway): %v", username, err)
	}

	score, _ := rand.Int(rand.Reader, big.NewInt(5001))
	scoreVal := score.Int64()
	if err := submitScore(token, scoreVal); err != nil {
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
				if err := submitScore(token, scoreVal); err != nil {
					log.Printf("[%s] Score update failed: %v", username, err)
				}
			}
		}
	}
}

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

func submitScore(token string, score int64) error {
	// gRPC-Gateway protojson accepts both number and string for int64,
	// but string is the canonical wire format.
	payload, _ := json.Marshal(map[string]any{
		"score":    fmt.Sprintf("%d", score),
		"subscore": "0",
	})
	url := fmt.Sprintf("%s/v2/leaderboard/%s", nakamaHost, leaderboardID)
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

func fetchLeaderboard(token string) *leaderboardRecordList {
	url := fmt.Sprintf("%s/v2/leaderboard/%s?limit=10", nakamaHost, leaderboardID)
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
		log.Printf("Fetch leaderboard failed: status %d: %s", resp.StatusCode, body.String())
		return nil
	}

	var list leaderboardRecordList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Printf("Decode leaderboard failed: %v", err)
		return nil
	}
	return &list
}

func printLeaderboard(list *leaderboardRecordList) {
	// Move cursor to home without clearing (avoids flicker).
	fmt.Print("\033[H\033[2K")
	fmt.Println("=== LEADERBOARD === (Ctrl+C to quit)")
	fmt.Print("\033[K")
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
