package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// hub fans out "something changed" notifications to every connected client.
// Messages are tiny JSON envelopes ({"type":"names.changed"}); the client is
// responsible for re-fetching whatever view it currently displays so the
// per-voter projection (my_vote, my_flag, sort/filter, admin status) stays
// correct without the server having to know what each client is showing.
var hub = struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
}{clients: map[*wsClient]struct{}{}}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Same-origin only: browsers send Origin; non-browser callers (curl) omit
	// it. Reject cross-origin upgrades to keep CSRF off the table.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		host := r.Host
		// Strip scheme to compare against Host.
		for _, p := range []string{"http://", "https://"} {
			if len(origin) > len(p) && origin[:len(p)] == p {
				return origin[len(p):] == host
			}
		}
		return false
	},
}

const (
	wsWriteWait      = 10 * time.Second
	wsPongWait       = 60 * time.Second
	wsPingPeriod     = (wsPongWait * 9) / 10
	wsMaxMessageSize = 512
	wsSendBuffer     = 8
)

// WSHandler upgrades the HTTP connection and registers the client. The voter
// cookie is already set by the VoterIdentity middleware that ran before us,
// so no extra identity work is needed here; the cookie travels with the
// websocket handshake and persists for the connection.
func WSHandler(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &wsClient{conn: conn, send: make(chan []byte, wsSendBuffer)}
	hub.mu.Lock()
	hub.clients[client] = struct{}{}
	hub.mu.Unlock()

	go client.writePump()
	go client.readPump()
}

// BroadcastChange notifies every connected client that the names list has
// changed. Slow clients are dropped rather than blocking the broadcast.
func BroadcastChange() {
	msg := []byte(`{"type":"names.changed"}`)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for client := range hub.clients {
		select {
		case client.send <- msg:
		default:
			// Drop the slow client; writePump will close it on send chan close.
			close(client.send)
			delete(hub.clients, client)
		}
	}
}

func removeClient(client *wsClient) {
	hub.mu.Lock()
	if _, ok := hub.clients[client]; ok {
		delete(hub.clients, client)
		close(client.send)
	}
	hub.mu.Unlock()
}

func (c *wsClient) readPump() {
	defer func() {
		removeClient(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(wsMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})
	for {
		// We don't expect inbound messages; just drain so pongs/closes are seen.
		if _, _, err := c.conn.NextReader(); err != nil {
			return
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
