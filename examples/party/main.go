// Nakama Party + Matchmaker client example.
//
// Spawns 10 players that each connect via WebSocket, form parties of 3
// after random delays, and the party leader starts matchmaking when the
// party is full. After the matchmaker creates a match, players join it
// and exchange hello messages.
//
// Usage:
//
//	go run ./examples/party/
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
	partySize  = 3
	maxWait    = 15 * time.Second
)

type role int

const (
	roleCreate role = iota
	roleJoin
	roleSolo
)

// partySlot coordinates a party being formed.
type partySlot struct {
	mu      sync.Mutex
	partyID string
	ready   chan struct{} // closed when partyID is set by leader
	joined  chan struct{}
}

// coordinator assigns players to parties.
type coordinator struct {
	mu    sync.Mutex
	seq   int
	slots []*partySlot
}

func (c *coordinator) assign() (slot *partySlot, r role, partyNum int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := c.seq
	c.seq++

	pn := n / partySize
	pos := n % partySize

	// Last incomplete group goes solo.
	if n >= numPlayers-numPlayers%partySize {
		return nil, roleSolo, 0
	}

	for len(c.slots) <= pn {
		c.slots = append(c.slots, &partySlot{
			ready:  make(chan struct{}),
			joined: make(chan struct{}, partySize),
		})
	}

	if pos == 0 {
		return c.slots[pn], roleCreate, pn
	}
	return c.slots[pn], roleJoin, pn
}

func main() {
	log.Printf("Starting %d players, party size = %d", numPlayers, partySize)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 90*time.Second)
	defer timeoutCancel()

	coord := &coordinator{}

	var wg sync.WaitGroup
	for i := range numPlayers {
		wg.Add(1)
		go runPlayer(timeoutCtx, &wg, i, coord)
	}

	wg.Wait()
	log.Println("All players finished.")
}

type playerEvents struct {
	party         chan *rtapi.Party
	partyPresence chan *rtapi.PartyPresenceEvent
	ticket        chan string
	matched       chan *rtapi.MatchmakerMatched
	match         chan *rtapi.Match
	data          chan *rtapi.MatchData
	matchPresence chan *rtapi.MatchPresenceEvent
}

func runPlayer(ctx context.Context, wg *sync.WaitGroup, id int, coord *coordinator) {
	defer wg.Done()

	deviceID := genDeviceID()
	tag := fmt.Sprintf("[P%d]", id)

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
		party:         make(chan *rtapi.Party, 5),
		partyPresence: make(chan *rtapi.PartyPresenceEvent, 5),
		ticket:        make(chan string, 1),
		matched:       make(chan *rtapi.MatchmakerMatched, 1),
		match:         make(chan *rtapi.Match, 1),
		data:          make(chan *rtapi.MatchData, 5),
		matchPresence: make(chan *rtapi.MatchPresenceEvent, 5),
	}

	go readLoop(ctx, conn, tag, ev)

	// Random delay before joining the game.
	delay := time.Duration(mathrand.Int64N(int64(maxWait)))
	log.Printf("%s waiting %v before starting", tag, delay.Round(time.Millisecond))
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}

	slot, r, partyNum := coord.assign()

	switch r {
	case roleCreate:
		runPartyLeader(ctx, conn, tag, ev, slot, partyNum)
	case roleJoin:
		runPartyMember(ctx, conn, tag, ev, slot, partyNum)
	case roleSolo:
		runSolo(ctx, conn, tag, ev)
	}
}

func runPartyLeader(ctx context.Context, conn *websocket.Conn, tag string, ev *playerEvents, slot *partySlot, partyNum int) {
	// Create the party.
	sendPartyCreate(conn)
	log.Printf("%s creating party #%d", tag, partyNum)

	// Wait for Party response with party_id.
	var partyID string
	select {
	case <-ctx.Done():
		return
	case p := <-ev.party:
		partyID = p.PartyId
		log.Printf("%s party #%d created: %s", tag, partyNum, partyID)
	}

	// Share party_id with joiners.
	slot.mu.Lock()
	slot.partyID = partyID
	slot.mu.Unlock()
	close(slot.ready)

	// Leader is first member confirmed.
	slot.joined <- struct{}{}

	// Wait for all members to confirm they joined.
	for range partySize - 1 {
		select {
		case <-ctx.Done():
			return
		case <-slot.joined:
		}
	}

	log.Printf("%s party #%d full, starting matchmaker", tag, partyNum)

	// Leader initiates party matchmaker.
	sendPartyMatchmakerAdd(conn, partyID)

	// Wait for match and exchange data.
	waitForMatch(ctx, conn, tag, ev, partyNum)
}

func runPartyMember(ctx context.Context, conn *websocket.Conn, tag string, ev *playerEvents, slot *partySlot, partyNum int) {
	// Wait for leader to share party_id.
	select {
	case <-ctx.Done():
		return
	case <-slot.ready:
	}

	slot.mu.Lock()
	partyID := slot.partyID
	slot.mu.Unlock()

	log.Printf("%s joining party #%d (%s...)", tag, partyNum, partyID[:12])

	sendPartyJoin(conn, partyID)

	// Wait for Party confirmation.
	select {
	case <-ctx.Done():
		return
	case p := <-ev.party:
		log.Printf("%s joined party #%d (members=%d)", tag, partyNum, len(p.Presences))
	}

	// Signal leader that we joined.
	slot.joined <- struct{}{}

	// Wait for match and exchange data.
	waitForMatch(ctx, conn, tag, ev, partyNum)
}

func runSolo(ctx context.Context, conn *websocket.Conn, tag string, ev *playerEvents) {
	log.Printf("%s no full party available, joining matchmaker solo (min=%d max=%d)", tag, partySize, partySize)

	sendMatchmakerAdd(conn)

	// Wait for match with timeout — solo player needs 2 more players
	// who may not arrive.
	matchTimeout := 15 * time.Second
	var ticket string
	select {
	case <-ctx.Done():
		return
	case ticket = <-ev.ticket:
		log.Printf("%s got ticket %s...", tag, ticket[:12])
	case <-time.After(matchTimeout):
		log.Printf("%s timed out waiting for match (no other solo players)", tag)
		return
	}

	// Wait for matched or timeout.
	select {
	case <-ctx.Done():
		return
	case matched := <-ev.matched:
		log.Printf("%s matched! users=%d", tag, len(matched.Users))
		// Fall through to join match below.
		var matchID, joinToken string
		switch mid := matched.Id.(type) {
		case *rtapi.MatchmakerMatched_MatchId:
			matchID = mid.MatchId
		case *rtapi.MatchmakerMatched_Token:
			joinToken = mid.Token
		}
		sendMatchJoin(conn, matchID, joinToken)
		select {
		case <-ctx.Done():
			return
		case m := <-ev.match:
			log.Printf("%s entered match %s (size=%d)", tag, m.MatchId, m.Size)
			sendMatchData(conn, m.MatchId, 1, []byte(fmt.Sprintf("Hello from solo player %s", tag)))
		}
	case <-time.After(matchTimeout):
		log.Printf("%s timed out waiting for match", tag)
		return
	}

	// Listen for data briefly.
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-ev.data:
			log.Printf("%s got data (op=%d): %s", tag, d.OpCode, string(d.Data))
		case <-ev.matchPresence:
		}
	}
}

func waitForMatch(ctx context.Context, conn *websocket.Conn, tag string, ev *playerEvents, partyNum int) {
	// Wait for matchmaker ticket.
	var ticket string
	select {
	case <-ctx.Done():
		return
	case ticket = <-ev.ticket:
		log.Printf("%s got ticket %s...", tag, ticket[:12])
	}

	// Wait for matchmaker matched.
	var matched *rtapi.MatchmakerMatched
	select {
	case <-ctx.Done():
		return
	case matched = <-ev.matched:
	}

	var matchID, joinToken string
	switch mid := matched.Id.(type) {
	case *rtapi.MatchmakerMatched_MatchId:
		matchID = mid.MatchId
		log.Printf("%s matched! match_id=%s users=%d", tag, matchID, len(matched.Users))
	case *rtapi.MatchmakerMatched_Token:
		joinToken = mid.Token
		log.Printf("%s matched! token=%s... users=%d", tag, joinToken[:12], len(matched.Users))
	}

	// Join the match.
	sendMatchJoin(conn, matchID, joinToken)

	select {
	case <-ctx.Done():
		return
	case m := <-ev.match:
		log.Printf("%s entered match %s (size=%d)", tag, m.MatchId, m.Size)
		matchID = m.MatchId
	}

	// Send a hello message.
	msg := fmt.Sprintf("Hello from player %s", tag)
	if partyNum >= 0 {
		msg = fmt.Sprintf("Hello from player %s (party #%d)", tag, partyNum)
	}
	sendMatchData(conn, matchID, 1, []byte(msg))

	// Listen for match data and presence events.
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
		case p := <-ev.matchPresence:
			joinIDs := make([]string, len(p.Joins))
			for i, j := range p.Joins {
				joinIDs[i] = j.Username
			}
			leaveIDs := make([]string, len(p.Leaves))
			for i, l := range p.Leaves {
				leaveIDs[i] = l.Username
			}
			log.Printf("%s match presence: joins=%v leaves=%v", tag, joinIDs, leaveIDs)
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
		case *rtapi.Envelope_Party:
			select {
			case ev.party <- msg.Party:
			default:
			}
		case *rtapi.Envelope_PartyPresenceEvent:
			select {
			case ev.partyPresence <- msg.PartyPresenceEvent:
			default:
			}
		case *rtapi.Envelope_PartyMatchmakerTicket:
			select {
			case ev.ticket <- msg.PartyMatchmakerTicket.Ticket:
			default:
			}
		case *rtapi.Envelope_MatchmakerMatched:
			select {
			case ev.matched <- msg.MatchmakerMatched:
			default:
			}
		case *rtapi.Envelope_MatchmakerTicket:
			select {
			case ev.ticket <- msg.MatchmakerTicket.Ticket:
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
			case ev.matchPresence <- msg.MatchPresenceEvent:
			default:
			}
		case *rtapi.Envelope_StatusPresenceEvent:
			// Initial status sync, ignored.
		case *rtapi.Envelope_Pong:
			// Ignore pongs.
		case *rtapi.Envelope_PartyClose:
			log.Printf("%s party closed: %s", tag, msg.PartyClose.PartyId)
		case *rtapi.Envelope_Error:
			log.Printf("%s server error: code=%d msg=%s", tag, msg.Error.Code, msg.Error.Message)
		}
	}
}

func sendPartyCreate(conn *websocket.Conn) {
	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_PartyCreate{
			PartyCreate: &rtapi.PartyCreate{
				Open:    true,
				MaxSize: partySize,
			},
		},
	}
	data, _ := proto.Marshal(env)
	conn.WriteMessage(websocket.BinaryMessage, data)
}

func sendPartyJoin(conn *websocket.Conn, partyID string) {
	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_PartyJoin{
			PartyJoin: &rtapi.PartyJoin{
				PartyId: partyID,
			},
		},
	}
	data, _ := proto.Marshal(env)
	conn.WriteMessage(websocket.BinaryMessage, data)
}

func sendPartyMatchmakerAdd(conn *websocket.Conn, partyID string) {
	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_PartyMatchmakerAdd{
			PartyMatchmakerAdd: &rtapi.PartyMatchmakerAdd{
				PartyId:  partyID,
				MinCount: partySize,
				MaxCount: partySize,
				Query:    "*",
			},
		},
	}
	data, _ := proto.Marshal(env)
	conn.WriteMessage(websocket.BinaryMessage, data)
}

func sendMatchmakerAdd(conn *websocket.Conn) {
	env := &rtapi.Envelope{
		Message: &rtapi.Envelope_MatchmakerAdd{
			MatchmakerAdd: &rtapi.MatchmakerAdd{
				MinCount: partySize,
				MaxCount: partySize,
				Query:    "*",
			},
		},
	}
	data, _ := proto.Marshal(env)
	conn.WriteMessage(websocket.BinaryMessage, data)
}

func sendMatchJoin(conn *websocket.Conn, matchID, token string) {
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
	conn.WriteMessage(websocket.BinaryMessage, data)
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
	conn.WriteMessage(websocket.BinaryMessage, data)
}

func genDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
