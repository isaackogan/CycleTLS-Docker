package e2e

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestE2E verifies that the CycleTLS WebSocket server (running locally,
// typically inside the Docker container under test) accepts a JSON request
// frame and emits a CycleTLS-format binary response stream ending in an "end"
// frame. The wire format is:
//
//	[uint16 BE: idLen][requestID][uint16 BE: typeLen][type]<type-specific payload>
//
// where type is one of: "response", "data", "chunk", "end", "error", "redirect".
func TestE2E(t *testing.T) {
	url := os.Getenv("CYCLETLS_WS_URL")
	if url == "" {
		url = "ws://127.0.0.1:9112/"
	}

	var (
		conn *websocket.Conn
		err  error
	)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, _, err = websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	defer conn.Close()

	const reqID = "e2e-1"
	req := map[string]interface{}{
		"requestId": reqID,
		"options": map[string]interface{}{
			"url":       "https://httpbin.org/get",
			"method":    "GET",
			"ja3":       "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27,29-23-24,0",
			"userAgent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		},
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	gotResponse := false
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read message: %v", err)
		}
		id, ftype, payload, perr := parseFrame(msg)
		if perr != nil {
			t.Fatalf("parse frame: %v", perr)
		}
		if id != reqID {
			t.Fatalf("unexpected requestId %q", id)
		}
		switch ftype {
		case "response":
			if len(payload) < 2 {
				t.Fatal("response frame missing status")
			}
			status := binary.BigEndian.Uint16(payload[:2])
			if status != 200 {
				t.Fatalf("expected status 200, got %d", status)
			}
			gotResponse = true
		case "error":
			t.Fatalf("server returned error frame: %q", string(payload))
		case "end":
			if !gotResponse {
				t.Fatal("stream ended before a response frame arrived")
			}
			return
		}
	}
}

func parseFrame(msg []byte) (id, ftype string, payload []byte, err error) {
	if len(msg) < 4 {
		return "", "", nil, errShort
	}
	idLen := int(binary.BigEndian.Uint16(msg[0:2]))
	if len(msg) < 2+idLen+2 {
		return "", "", nil, errShort
	}
	id = string(msg[2 : 2+idLen])
	off := 2 + idLen
	typeLen := int(binary.BigEndian.Uint16(msg[off : off+2]))
	off += 2
	if len(msg) < off+typeLen {
		return "", "", nil, errShort
	}
	ftype = string(msg[off : off+typeLen])
	off += typeLen
	payload = msg[off:]
	return id, ftype, payload, nil
}

type frameErr string

func (e frameErr) Error() string { return string(e) }

const errShort = frameErr("frame truncated")
