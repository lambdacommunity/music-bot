package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/diamondburned/arikawa/v3/voice"
	"github.com/diamondburned/arikawa/v3/voice/voicegateway"
	"github.com/diamondburned/oggreader"
	"github.com/kkdai/youtube/v2"
	"github.com/pkg/errors"

	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/utils/bot"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("error loading .env file")
	}

	b := &Bot{
		YoutubeClient: youtube.Client{},
	}

	if os.Getenv("PROFILE") != "" {
		go func() {
			for {
				time.Sleep(time.Second * 5)
				PrintMemUsage()
				time.Sleep(time.Minute)
			}
		}()
	}

	b.InitRoutines()
	bot.Run(os.Getenv("DISCORD_TOKEN"), b,
		func(ctx *bot.Context) error {
			voice.AddIntents(b.Ctx.State.Gateway)
			ctx.HasPrefix = bot.NewPrefix("!")
			return nil
		},
	)
}

type SessionEventType uint8

const (
	InitSessionEvent           SessionEventType = 0
	DisconnChannelSessionEvent SessionEventType = 1
	ConnChannelSessionEvent    SessionEventType = 2
)

type SessionEvent struct {
	EventCode SessionEventType
	Payload   interface{}
}

type Bot struct {
	Ctx           *bot.Context
	YoutubeClient youtube.Client

	session          *voice.Session
	sessionChan      chan SessionEvent
	sessionReplyChan chan error
}

func (b *Bot) Send(code SessionEventType, payload interface{}) error {
	b.sessionChan <- SessionEvent{
		EventCode: code,
		Payload:   payload,
	}
	return <-b.sessionReplyChan
}

func (b *Bot) InitRoutines() {
	// Session routine
	b.sessionChan = make(chan SessionEvent)
	b.sessionReplyChan = make(chan error, 1)

	go func() {
		for {
			var err error
			var event SessionEvent
			select {
			case event = <-b.sessionChan:
				switch event.EventCode {
				case InitSessionEvent:
					b.session, err = voice.NewSession(b.Ctx.State)
					if err != nil {
						err = errors.Wrap(err, "failed to create voice session")
					}
				case DisconnChannelSessionEvent:
					err = b.session.Leave()
					if err != nil {
						err = errors.Wrap(err, "failed to leave channel")
					}
				case ConnChannelSessionEvent:
					event := event.Payload.(*gateway.MessageCreateEvent)
					voiceState, err := b.Ctx.State.VoiceState(event.GuildID, event.Author.ID)
					if err != nil {
						err = errors.Wrap(err, "failed to create voice state")
					}

					if err = b.session.JoinChannel(event.GuildID, voiceState.ChannelID, false, true); err != nil {
						err = errors.Wrap(err, "failed to join channel")
					}
				}
			}

			b.sessionReplyChan <- err
			time.Sleep(time.Millisecond * 250)
		}
	}()
}

func (b *Bot) Disconnect(e *gateway.MessageCreateEvent) error {
	return b.Send(DisconnChannelSessionEvent, nil)
}

func (b *Bot) Dc(e *gateway.MessageCreateEvent) error {
	return b.Disconnect(e)
}

var m = map[string]*pausableReader{}

func (b *Bot) Play(e *gateway.MessageCreateEvent) error {
	video, err := b.YoutubeClient.GetVideo(e.Message.Content)
	if err != nil {
		return errors.Wrap(err, "failed to get youtube video")
	}

	formats := video.Formats.Type("audio").WithAudioChannels()
	formats.Sort()
	stream, _, err := b.YoutubeClient.GetStream(video, &formats[0])
	if err != nil {
		return errors.Wrap(err, "failed to get youtube audio stream")
	}

	ffmpeg := exec.Command(
		"ffmpeg",
		// Streaming is slow, so a single thread is all we need.
		"-hide_banner", "-threads", "1", "-loglevel", "error",
		// Input file. This should be changed.
		"-i", "-",
		// Output format; leave as "libopus".
		"-c:a", "libopus",
		// Bitrate in kilobits. This doesn't matter, but I recommend 96k as the
		// sweet spot.
		"-b:a", "96k",
		// Frame duration should be the same as what's given into
		// udp.ResetFrequency.
		"-frame_duration", "60",
		// Disable variable bitrate to keep packet sizes consistent. This is
		// optional, but it technically reduces stuttering.
		"-vbr", "off",
		// Output format, which is opus, so we need to unwrap the opus file.
		"-f", "opus", "-",
	)
	ffmpeg.Stdin = stream
	ffmpeg.Stderr = os.Stderr

	stdout, err := ffmpeg.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "failed to create pipe between bot and ffmpeg")
	}

	if err = ffmpeg.Start(); err != nil {
		return errors.Wrap(err, "failed to start ffmpeg")
	}

	if b.session == nil {
		err = b.Send(InitSessionEvent, nil)
		if err != nil {
			return err
		}
	}

	if b.session.VoiceUDPConn() == nil {
		err = b.Send(ConnChannelSessionEvent, e)
		if err != nil {
			return err
		}
	}

	in := b.session.VoiceUDPConn()
	in.ResetFrequency(60*time.Millisecond, 2880)

	if err := b.session.Speaking(voicegateway.Microphone); err != nil {
		return errors.Wrap(err, "failed to send speaking")
	}

	out := &pausableReader{
		r: stdout,
	}

	m[e.GuildID.String()] = out
	if err := oggreader.DecodeBuffered(in, out); err != nil {
		return errors.Wrap(err, "failed to decode ogg")
	}

	return nil
}

func (b *Bot) Pause(e *gateway.MessageCreateEvent) {
	if p, ok := m[e.GuildID.String()]; ok {
		p.Pause()
	}
}

type pausableReader struct {
	r       io.Reader
	unpause chan struct{}
	pauseMu sync.Mutex
}

func (p *pausableReader) Pause() {
	p.pauseMu.Lock()
	if p.unpause != nil {
		close(p.unpause)
	}
	p.unpause = make(chan struct{})
	p.pauseMu.Unlock()
}

func (p *pausableReader) Read(b []byte) (int, error) {
	p.pauseMu.Lock()
	ch := p.unpause
	p.pauseMu.Unlock()

	if p.unpause != nil {
		<-ch
	}

	return p.r.Read(b)
}

// PrintMemUsage outputs the current, total and OS memory being used. As well as the number
// of garage collection cycles completed.
func PrintMemUsage() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// For info on each, see: https://golang.org/pkg/runtime/#MemStats
	fmt.Printf("Alloc = %v MiB", bToMb(m.Alloc))
	fmt.Printf("\tTotalAlloc = %v MiB", bToMb(m.TotalAlloc))
	fmt.Printf("\tSys = %v MiB", bToMb(m.Sys))
	fmt.Printf("\tNumGC = %v\n", m.NumGC)
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}
