package srv

// This file proves the SERVER half of multi-client coherence (issue
// strudel-agent-3vo.8): POST /api/anchor, the guard on what an agent may
// publish as the shared timeline, and the frame that tells every listener to
// adopt it.
//
// The anchor is the entire cross-machine mechanism — strudel has no clock to
// align to — so the load-bearing properties are all about refusing to publish a
// timeline no listener could use, and never announcing one that was not stored:
//
//   - an accepted re-anchor broadcasts exactly one `anchor` frame, and it does
//     not bump the version (it moves where the shared timeline starts, not what
//     is playing);
//   - a REFUSED re-anchor broadcasts NOTHING and leaves the stored anchor
//     untouched, so no listener is ever told to adopt a bar grid the server
//     itself rejected;
//   - the response is byte-identical to GET /api/state, so the agent and the
//     listeners cannot disagree about which timeline is live.
//
// The client half (cycle-aligned commit, sync status) lives in
// strudel-agent-3vo.8.2 and .8.5 and adds to this file.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// anchorBody builds a POST /api/anchor body. epochMS is passed in rather than
// defaulted to "now" because the skew cases need to be far from it, and because
// the acceptance cases must NOT use a pinned literal: the server refuses a
// skewed epoch, so a constant would make an acceptance assertion pass only by
// being refused, which would prove nothing about acceptance.
func anchorBody(epochMS int64, cps float64) string {
	return fmt.Sprintf(`{"epochMs":%d,"cps":%g}`, epochMS, cps)
}

// TestAnchorRouteRejectsNonsenseAnchor pins the refusal half of the contract,
// and the property that makes a refusal safe: a refused re-anchor must change
// NOTHING. A handler that stored the new anchor and then complained would leave
// the stored timeline and the reported one disagreeing, and every listener would
// be scheduling against a grid the agent never got an acknowledgement for.
func TestAnchorRouteRejectsNonsenseAnchor(t *testing.T) {
	_, base, _ := fanoutServer(t)

	// Seed a known-good anchor, so the "unchanged" assertions below are about a
	// non-default value rather than the constructor's.
	good := time.Now().UnixMilli()
	if code, raw := fanoutPost(t, base, "/api/anchor", anchorBody(good, 0.75)); code != http.StatusOK {
		t.Fatalf("seeding a valid anchor: %d %q", code, bodyOf(raw))
	}

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "zero rate",
			body: anchorBody(time.Now().UnixMilli(), 0),
			want: "cps must be greater than 0",
		},
		{
			name: "negative rate",
			body: anchorBody(time.Now().UnixMilli(), -1),
			want: "cps must be greater than 0",
		},
		{
			// A unit mix-up is the realistic accident: 0.5 cycles per MINUTE.
			name: "rate above the ceiling",
			body: anchorBody(time.Now().UnixMilli(), AnchorMaxCPS*1000),
			want: "cps must be at most",
		},
		{
			name: "epoch far in the past",
			body: anchorBody(time.Now().UnixMilli()-AnchorMaxSkewMS-60_000, DefaultCPS),
			want: "epochMs must be within",
		},
		{
			name: "epoch far in the future",
			body: anchorBody(time.Now().UnixMilli()+AnchorMaxSkewMS+60_000, DefaultCPS),
			want: "epochMs must be within",
		},
		{
			// Both fields are required. An absent epochMs must not be read as
			// zero (a 1970 epoch, wildly skewed) and must not silently keep the
			// previous one: a re-anchor replaces the whole timeline.
			name: "missing epochMs",
			body: `{"cps":0.5}`,
			want: "epochMs must be within",
		},
		{
			name: "missing cps",
			body: fmt.Sprintf(`{"epochMs":%d}`, time.Now().UnixMilli()),
			want: "cps must be greater than 0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := fanoutPost(t, base, "/api/anchor", tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("POST /api/anchor %s = %d %q, want 400", tc.body, code, bodyOf(raw))
			}
			if !strings.Contains(raw, tc.want) {
				t.Errorf("error %q does not say %q: the message must name the constraint so an agent can act on it", bodyOf(raw), tc.want)
			}

			// The refusal must be total: the stored timeline is untouched.
			code, raw = fanoutGet(t, base, "/api/state")
			if code != http.StatusOK {
				t.Fatalf("GET /api/state after a refused re-anchor: %d %q", code, bodyOf(raw))
			}
			var snap Snapshot
			if err := json.Unmarshal([]byte(raw), &snap); err != nil {
				t.Fatalf("decode state: %v (%q)", err, bodyOf(raw))
			}
			if snap.Anchor.EpochMS != good || snap.Anchor.CPS != 0.75 {
				t.Errorf("a refused re-anchor changed the stored anchor to %+v, want {%d 0.75}", snap.Anchor, good)
			}
			if snap.Version != 0 {
				t.Errorf("a refused re-anchor changed the version to %d, want 0", snap.Version)
			}
		})
	}
}

// TestAnchorRouteAcceptedBroadcastsOneAnchorFrame is the positive half: an
// accepted re-anchor reaches every listener as exactly one `anchor` frame
// carrying the new timeline.
//
// Exactly one matters as much as at least one. The listener-count frame is
// excluded from the listener that caused it, for a documented reason; there is
// no such reason here, because a listener that misses the re-anchor keeps
// scheduling against the OLD grid while its peers move — which is precisely the
// incoherence this endpoint exists to fix.
func TestAnchorRouteAcceptedBroadcastsOneAnchorFrame(t *testing.T) {
	s, base, wsBase := fanoutServer(t)

	a, _ := wsDial(t, wsBase, nil)
	b, _ := wsDial(t, wsBase, nil)

	before := s.Conductor.Snapshot()
	la := fanoutAttach(t, "a", a, before)
	// b's arrival broadcasts a count frame to a. It is not what this test is
	// about, so it is drained explicitly rather than left to be misread as the
	// anchor frame.
	la.drainListenerCounts(t, 1, 2, "b joining")
	lb := fanoutAttach(t, "b", b, before)

	epoch := time.Now().UnixMilli()
	code, raw := fanoutPost(t, base, "/api/anchor", anchorBody(epoch, 1.25))
	if code != http.StatusOK {
		t.Fatalf("POST /api/anchor: %d %q", code, bodyOf(raw))
	}

	for _, l := range []*fanoutListener{la, lb} {
		ev := l.next(t, l.name+": anchor frame")
		if ev.Kind != EventAnchor {
			t.Fatalf("%s: frame kind = %q, want %q", l.name, ev.Kind, EventAnchor)
		}
		if ev.Snapshot.Anchor.EpochMS != epoch || ev.Snapshot.Anchor.CPS != 1.25 {
			t.Errorf("%s: frame carries anchor %+v, want {%d 1.25}", l.name, ev.Snapshot.Anchor, epoch)
		}
		// A re-anchor moves where the shared timeline starts; it is not a new
		// revision of the music, so it must not enter the code history.
		if ev.Snapshot.Version != before.Version {
			t.Errorf("%s: re-anchor moved the version from %d to %d: a timeline move is not a new code document",
				l.name, before.Version, ev.Snapshot.Version)
		}
		// No second copy, and no count frame provoked by the write itself.
		l.expectNone(t, l.name+" after the anchor frame")
	}
}

// TestAnchorRouteRejectedBroadcastsNothing is the negative fan-out case, and it
// is the one that needs a real socket rather than a state read.
//
// A rejected request that still broadcast would leave every listener believing
// it had adopted a new timeline. Because each listener re-derives its position
// from the anchor it was last told, that belief is invisible to it: it does not
// error, it simply starts committing to the wrong bar, and the drift surfaces
// only later as music that does not line up. Nothing on the wire distinguishes
// "adopted" from "adopted a lie", so the only place to catch it is here.
func TestAnchorRouteRejectedBroadcastsNothing(t *testing.T) {
	s, base, wsBase := fanoutServer(t)

	conn, _ := wsDial(t, wsBase, nil)
	before := s.Conductor.Snapshot()
	l := fanoutAttach(t, "listener", conn, before)

	// Every refusal mode, in one quiet window: bad rate, negative rate, skewed
	// epoch, a missing field, and an undocumented one.
	for _, body := range []string{
		anchorBody(time.Now().UnixMilli(), 0),
		anchorBody(time.Now().UnixMilli(), -2),
		anchorBody(time.Now().UnixMilli()-AnchorMaxSkewMS-60_000, DefaultCPS),
		`{"cps":0.5}`,
		`{"epochMs":1757000000000,"cps":0.5,"surprise":true}`,
	} {
		if code, raw := fanoutPost(t, base, "/api/anchor", body); code != http.StatusBadRequest {
			t.Fatalf("POST /api/anchor %s = %d %q, want 400 so the no-broadcast rule is exercised", body, code, bodyOf(raw))
		}
	}
	l.expectNone(t, "refused re-anchors")
}

// TestAnchorRouteResponseIsTheSameSnapshot proves the agent and the listeners
// read the same timeline. The re-anchor response is produced by the same encoder
// as GET /api/state and broadcast as the same frame, so byte equality is the
// check: an agent that re-anchors and then reads /api/state must not see a
// different epoch than the one its own 200 reported.
func TestAnchorRouteResponseIsTheSameSnapshot(t *testing.T) {
	_, base, _ := fanoutServer(t)

	epoch := time.Now().UnixMilli()
	code, posted := fanoutPost(t, base, "/api/anchor", anchorBody(epoch, 2))
	if code != http.StatusOK {
		t.Fatalf("POST /api/anchor: %d %q", code, bodyOf(posted))
	}
	code, state := fanoutGet(t, base, "/api/state")
	if code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", code, bodyOf(state))
	}
	if posted != state {
		t.Errorf("the re-anchor response and GET /api/state disagree:\n POST: %s\n  GET: %s", bodyOf(posted), bodyOf(state))
	}
}

// TestAnchorRouteRejectsMalformedAndOversizedBodies holds the new endpoint to
// the SAME body handling as every other /api route. It is tempting for a new
// handler to decode its own body, and the cap in particular is installed
// centrally precisely so that no endpoint can forget it: a handler reading
// r.Body without going through decodeBody would happily buffer an unbounded
// payload.
func TestAnchorRouteRejectsMalformedAndOversizedBodies(t *testing.T) {
	_, base, _ := fanoutServer(t)

	t.Run("unknown field", func(t *testing.T) {
		code, raw := fanoutPost(t, base, "/api/anchor",
			fmt.Sprintf(`{"epochMs":%d,"cps":0.5,"tempo":120}`, time.Now().UnixMilli()))
		if code != http.StatusBadRequest {
			t.Fatalf("unknown field = %d %q, want 400", code, bodyOf(raw))
		}
		if !strings.Contains(raw, "unknown field") {
			t.Errorf("error %q does not name the unknown field", bodyOf(raw))
		}
	})

	t.Run("trailing data", func(t *testing.T) {
		code, raw := fanoutPost(t, base, "/api/anchor",
			anchorBody(time.Now().UnixMilli(), 0.5)+` {"cps":99}`)
		if code != http.StatusBadRequest {
			t.Fatalf("trailing data = %d %q, want 400", code, bodyOf(raw))
		}
		if !strings.Contains(raw, "unexpected data after JSON body") {
			t.Errorf("error %q does not report trailing data", bodyOf(raw))
		}
	})

	t.Run("oversized", func(t *testing.T) {
		// Padded past APIMaxBodyBytes with an unknown field, so the ONLY reason
		// to refuse is the cap. A handler that validated before sizing would
		// answer 400 here and quietly prove nothing about the cap.
		padding := strings.Repeat("x", APIMaxBodyBytes)
		code, raw := fanoutPost(t, base, "/api/anchor",
			fmt.Sprintf(`{"epochMs":%d,"cps":0.5,"pad":%q}`, time.Now().UnixMilli(), padding))
		if code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized body = %d %q, want 413", code, bodyOf(raw))
		}
	})

	t.Run("wrong verb", func(t *testing.T) {
		// The Allow header is part of the contract, not a courtesy: it is what
		// tells an agent which verb to use, so it is read off a real response
		// rather than assumed.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/anchor", nil)
		if err != nil {
			t.Fatalf("build GET /api/anchor: %v", err)
		}
		client := &http.Client{Transport: boundedTransport(), Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/anchor: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET /api/anchor = %d, want 405", resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Errorf("405 Allow = %q, want %q", got, http.MethodPost)
		}
	})
}
