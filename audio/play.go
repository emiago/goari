package audio

import (
	"time"

	"github.com/CyCoreSystems/ari"
	v2 "github.com/CyCoreSystems/ari/v2"

	"golang.org/x/net/context"
)

// AllDTMF is a string which contains all possible
// DTMF digits.
const AllDTMF = "0123456789ABCD*#"

// PlaybackStartTimeout is the time to allow for Asterisk to
// send the PlaybackStarted before giving up.
var PlaybackStartTimeout = 1 * time.Second

// MaxPlaybackTime is the maximum amount of time to allow for
// a playback to complete.
var MaxPlaybackTime = 10 * time.Minute

// Play plays the audio to the given Player, waiting for the playback to finish or an error to be generated
func Play(ctx context.Context, bus ari.Subscriber, p Player, mediaURI string) error {
	pb, err := PlayAsync(bus, p, mediaURI)
	if err != nil {
		return err
	}
	defer pb.Cancel()

	select {
	case <-pb.StopCh():
	case <-ctx.Done():
		return ctx.Err()
	}

	return pb.Err()
}

// PlayAsync plays audio to the given Player, returning a Playback object
func PlayAsync(bus ari.Subscriber, p Player, mediaURI string) (*Playback, error) {

	var pb Playback

	// subscribe to ARI events
	s := bus.Subscribe("PlaybackStarted", "PlaybackFinished")

	// start playback
	h, err := p.Play(mediaURI)
	if err != nil {
		s.Cancel()
		return nil, err
	}

	// build return value

	quitCh := make(chan struct{})

	pb.handle = h
	pb.stopCh = make(chan struct{})
	pb.startCh = make(chan struct{})
	pb.quitCh = quitCh

	// get playback data/identifier

	// NOTE: this is where we may want to be able to access handle.ID directly?
	data, err := h.Data()
	if err != nil {
		s.Cancel()
		return nil, err
	}

	go func() {

		defer s.Cancel()

		id := data.ID

		// Wait for the playback to start
		startTimer := time.After(PlaybackStartTimeout)
	PlaybackStartLoop:
		for {
			select {
			case <-quitCh:
				close(pb.startCh)
				close(pb.stopCh)
				return
			case v := <-s.C:
				if v == nil {
					Logger.Debug("Nil event received")
					continue PlaybackStartLoop
				}
				switch v.GetType() {
				case "PlaybackStarted":
					e := v.(*v2.PlaybackStarted)
					if e.Playback.ID != id {
						Logger.Debug("Ignoring unrelated playback", "expected", id, "got", e.Playback.ID)
						continue PlaybackStartLoop
					}
					Logger.Debug("Playback started", "h", h)
					break PlaybackStartLoop
				case "PlaybackFinished":
					e := v.(*v2.PlaybackFinished)
					if e.Playback.ID != id {
						Logger.Debug("Ignoring unrelated playback")
						continue PlaybackStartLoop
					}
					Logger.Debug("Playback stopped (before PlaybackStated received)", "h", h)
					close(pb.startCh)
					close(pb.stopCh)
					return
				default:
					Logger.Debug("Unhandled e.Type", v.GetType())
					continue PlaybackStartLoop
				}
			case <-startTimer:
				Logger.Error("Playback timed out", "h", h)
				pb.err = timeoutErr{"Timeout waiting for start of playback"}
				close(pb.startCh)
				close(pb.stopCh)
				return
			}
		}

		// trigger playback start signal and defer playback stop signal
		close(pb.startCh)
		defer close(pb.stopCh)

		// Playback has started.  Wait for it to finish
		stopTimer := time.After(MaxPlaybackTime)
	PlaybackStopLoop:
		for {
			select {
			case <-quitCh:
				return
			case v := <-s.C:
				if v == nil {
					Logger.Debug("Nil event received")
					continue PlaybackStopLoop
				}
				switch v.GetType() {
				case "PlaybackFinished":
					e := v.(*v2.PlaybackFinished)
					if e.Playback.ID != id {
						Logger.Debug("Ignoring unrelated playback")
						continue PlaybackStopLoop
					}
					Logger.Debug("Playback stopped", "h", h)
					return
				default:
					Logger.Debug("Unhandled e.Type", v.GetType())
					continue PlaybackStopLoop
				}
			case <-stopTimer:
				Logger.Error("Playback timed out", "h", h)
				pb.err = timeoutErr{"Timeout waiting for stop of playback"}
				return
			}
		}
	}()

	return &pb, err
}

type timeoutErr struct {
	msg string
}

func (err timeoutErr) Error() string {
	return err.msg
}

func (err timeoutErr) Timeout() bool {
	return true
}