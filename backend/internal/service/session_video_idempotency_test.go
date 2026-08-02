package service

import (
	"bytes"
	"fmt"
	"testing"

	"backend/internal/model"
	"backend/internal/provider/adobe"
)

func TestHashSessionVideoPayloadIsStableAndParameterSensitive(t *testing.T) {
	a := hashSessionVideoPayload("firefly-video", " move ", "16:9", "1080p", "5s", [][]byte{[]byte("frame-a")})
	b := hashSessionVideoPayload("firefly-video", "move", "16:9", "1080p", "5s", [][]byte{bytes.Clone([]byte("frame-a"))})
	c := hashSessionVideoPayload("firefly-video", "move", "16:9", "1080p", "10s", [][]byte{[]byte("frame-a")})
	d := hashSessionVideoPayload("firefly-video", "move", "16:9", "1080p", "5s", [][]byte{[]byte("frame-b")})
	if a != b {
		t.Fatalf("equivalent payload hashes differ: %q != %q", a, b)
	}
	if a == c {
		t.Fatal("duration change did not change payload hash")
	}
	if a == d {
		t.Fatal("reference change did not change payload hash")
	}
}

func TestSessionVideoResponseMapsPendingReplayToQueued(t *testing.T) {
	event := &model.EventLog{
		ID: "evt-video", RequestID: "req-video", Kind: "video", Status: "pending",
		JobStage: "running", Cost: 25,
	}
	got := sessionVideoResponse(event, 75, true)
	if got["status"] != "queued" || got["event_id"] != event.ID || got["request_id"] != event.RequestID {
		t.Fatalf("unexpected replay response: %#v", got)
	}
	if got["replayed"] != true || got["credits"] != float64(75) || got["charged"] != float64(25) {
		t.Fatalf("unexpected replay accounting: %#v", got)
	}
}

func TestAdobeVideoUnknownOutcomeStopsPoolFailover(t *testing.T) {
	for _, sentinel := range []error{adobe.ErrVideoSubmitAmbiguous, adobe.ErrVideoTaskSubmitted} {
		err := fmt.Errorf("context: %w", sentinel)
		auth, quota, temporary, dead := adobeVideoErrClass(err)
		if auth || quota || temporary || dead {
			t.Fatalf("unsafe failover classification for %v: %v %v %v %v", sentinel, auth, quota, temporary, dead)
		}
	}

	auth, quota, temporary, dead := adobeVideoErrClass(adobe.ErrTemporaryUpstream)
	if auth || quota || !temporary || dead {
		t.Fatalf("explicit temporary rejection classification changed: %v %v %v %v", auth, quota, temporary, dead)
	}
}
