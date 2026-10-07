package protocol

import "testing"

func TestDecodeMessage(t *testing.T) {
	message, err := Decode([]byte(`{"type":"chat.send","payload":{"message":"hello"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if message.Type != "chat.send" || len(message.Payload) == 0 {
		t.Fatalf("unexpected message: %+v", message)
	}
}

func TestDecodeRejectsInvalidEnvelope(t *testing.T) {
	for _, input := range []string{
		`{"payload":{}}`,
		`{"type":"chat.send"}`,
		`{"type":"chat.send","payload":null}`,
		`not json`,
	} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Errorf("Decode(%q) unexpectedly succeeded", input)
		}
	}
}
