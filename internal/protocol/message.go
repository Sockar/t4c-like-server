package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

type Message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type IncomingMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func Decode(data []byte) (IncomingMessage, error) {
	var message IncomingMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return IncomingMessage{}, fmt.Errorf("decode message: %w", err)
	}
	if message.Type == "" {
		return IncomingMessage{}, errors.New("message type is required")
	}
	if len(message.Payload) == 0 || bytes.Equal(bytes.TrimSpace(message.Payload), []byte("null")) {
		return IncomingMessage{}, errors.New("message payload is required")
	}
	return message, nil
}
