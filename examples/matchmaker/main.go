// Nakama Matchmaker client example.
//
// Spawns 10 players that each connect via WebSocket, join the matchmaker
// after a random delay, and get matched when 3 players are available.
// After joining the match, players exchange hello messages.
//
// Usage:
//
//	go run ./examples/matchmaker/
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
	mathrand "math/rand/v2"
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
	nakamaHost = "http://localhost:7350"
	nakamaWS   = "ws://localhost:7350"
	serverKey  = "defaultkey"
	numPlayers = 10
	matchSize  = 3
	maxWait    = 10 * time.Second
)

func main() {
	log.Printf("Starting %d players, match size = %d", numPlayers, matchSize)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Timeout after all players should have been matched.
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 60*time.Second)
	defer timeoutCancel()

	var wg sync.WaitGroup
	for i := 0; i < numPlayers; i++ {
		wg.Add(1)
		go runPlayer(timeoutCtx, &wg, i)
	}

	wg.Wait()
	log.Println("All players finished.")
}

type playerEvents struct {
	ticket   chan string
	matched  chan *rtapi.MatchmakerMatched
	match    chan *rtapi.Match
	data     chan *rtapi.MatchData
	presence chan *rtapi.MatchPresenceEvent
}

func runPlayer(ctx context.Context, wg *sync.WaitGroup, id int) {
	defer wg.Done()

	deviceID := genDeviceID()
	tag := fmt.Sprintf("[Player %d]", id)

	token, err := authenticate(deviceID)
	if err != nil {
		log.Printf("%s auth failed: %v", tag, err)
		return
	}

	conn, err := connectWS(token)
	if err != nil {
		log.Printf("%s WS connect failed: %v", tag, err)
		return
	}
	defer conn.Close()

	ev := &playerEvents{
		ticket:   make(chan string, 1),
		matched:  make(chan *rtapi.MatchmakerMatched, 1),
		match:    make(chan *rtapi.Match, 1),
		data:     make(chan *rtapi.MatchData, 5),
		presence: make(chan *rtapi.MatchPresenceEvent, 5),
	}

	go readLoop(ctx, conn, tag, ev)

	// Random delay before entering matchmaker.
	delay := time.Duration(mathrand.Int64N(int64(maxWait)))
	log.Printf("%s waiting %v before matchmaker", tag, delay.Round(time.Millisecond))
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}

	// Step 1: Join matchmaker.
	sendMatchmakerAdd(conn, tag)
	log.Printf("%s joined matchmaker (min=%d max=%d)", tag, matchSize, matchSize)

	// Wait for ticket.
	var ticket string
	select {
	case <-ctx.Done():
		return
	case ticket = <-ev.ticket:
		log.Printf("%s got ticket %s", tag, ticket[:12])
	}

	// Wait for matched.
	var matched *rtapi.MatchmakerMatched
	select {
	case <-ctx.Done():
		return
	case matched = <-ev.matched:
	}

	// Determine match ID or token.
	var matchID, joinToken string
	switch id := matched.Id.(type) {
	case *rtapi.MatchmakerMatched_MatchId:
		matchID = id.MatchId
		log.Printf("%s matched! match_id=%s users=%d", tag, matchID, len(matched.Users))
	case *rtapi.MatchmakerMatched_Token:
		joinToken = id.Token
		log.Printf("%s matched! token=%s... users=%d", tag, joinToken[:12], len(matched.Users))
	}

	// Step 2: Join the match.
	sendMatchJoin(conn, tag, matchID, joinToken)
	log.Printf("%s joining match...", tag)

	// Wait for match confirmation.
	select {
	case <-ctx.Done():
		return
	case m := <-ev.match:
		log.Printf("%s entered match %s (size=%d)", tag, m.MatchId, m.Size)
		matchID = m.MatchId
	}

	// Step 3: Send a hello to everyone in the match.
	msg := fmt.Sprintf("Hello from player %d!", id)
	sendMatchData(conn, matchID, 1, []byte(msg))

	// Step 4: Listen for match data and presence events.
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-ev.data:
			sender := "unknown"
			if d.Presence != nil {
				sender = d.Presence.Username
			}
			log.Printf("%s got data from %s (op=%d): %s", tag, sender, d.OpCode, string(d.Data))
		case p := <-ev.presence:
			joinIDs := make([]string, len(p.Joins))
			for i, j := range p.Joins {
				joinIDs[i] = j.Username
			}
			leaveIDs := make([]string, len(p.Leaves))
			for i, l := range p.Leaves {
				leaveIDs[i] = l.Username
			}
			log.Printf("%s presence: joins=%v leaves=%v", tag, joinIDs, leaveIDs)
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
		return "", fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		return "", fmt.Errorf("%s: %s", resp.Status, body.String())
	}

	var session struct{ Token string }
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("decode: %w", err)
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

func readLoop(ctx context.Context, conn *websocket.Conn, tag string, ev *playerEvents) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("%s read error: %v", tag, err)
			}
			return
		}

		var env rtapi.Envelope
		if err := proto.Unmarshal(raw, &env); err != nil {
			continue
		}

		switch msg := env.Message.(type) {
		case *rtapi.Envelope_MatchmakerTicket:
			select {
			case ev.ticket <- msg.MatchmakerTicket.Ticket:
			default:
			}
		case *rtapi.Envelope_MatchmakerMatched:
			select {
			case ev.matched <- msg.MatchmakerMatched:
			default:
			}
		case *rtapi.Envelope_Match:
			select {
			case ev.match <- msg.Match:
			default:
			}
		case *rtapi.Envelope_MatchData:
			select {
			case ev.data <- msg.MatchData:
			default:
			}
		case *rtapi.Envelope_MatchPresenceEvent:
			select {
			case ev.presence <- msg.MatchPresenceEvent:
			default:
			}
		case *rtapi.Envelope_StatusPresenceEvent:
			// Initial status sync, ignored.
		case *rtapi.Envelope_Pong:
			// Ignore pongs.
		case *rtapi.Envelope_Error:
			log.Printf("%s server error: %v", tag, msg.Error.Message)
		}
	}
}

func sendMatchmakerAdd(conn *websocket.Conn, tag string) {
	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_MatchmakerAdd{
			MatchmakerAdd: &rtapi.MatchmakerAdd{
				MinCount: matchSize,
				MaxCount: matchSize,
				Query:    "",
			},
		},
	}
	data, _ := proto.Marshal(env)
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Printf("%s matchmaker add error: %v", tag, err)
	}
}

func sendMatchJoin(conn *websocket.Conn, tag, matchID, token string) {
	join := &rtapi.MatchJoin{}
	if matchID != "" {
		join.Id = &rtapi.MatchJoin_MatchId{MatchId: matchID}
	} else {
		join.Id = &rtapi.MatchJoin_Token{Token: token}
	}

	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_MatchJoin{MatchJoin: join},
	}
	data, _ := proto.Marshal(env)
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Printf("%s match join error: %v", tag, err)
	}
}

func sendMatchData(conn *websocket.Conn, matchID string, opCode int64, payload []byte) {
	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_MatchDataSend{
			MatchDataSend: &rtapi.MatchDataSend{
				MatchId: matchID,
				OpCode:  opCode,
				Data:    payload,
			},
		},
	}
	data, _ := proto.Marshal(env)
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Printf("match data send error: %v", err)
	}
}

func genDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
