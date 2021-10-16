package main

import (
	"log"
	"os"
	"os/exec"
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

	bot.Run(os.Getenv("DISCORD_TOKEN"), b,
		func(ctx *bot.Context) error {
			voice.AddIntents(b.Ctx.State.Gateway)
			ctx.HasPrefix = bot.NewPrefix("!")
			return nil
		},
	)
}

type Bot struct {
	Ctx           *bot.Context
	YoutubeClient youtube.Client
}

func (b *Bot) Play(e *gateway.MessageCreateEvent) error {
	video, err := b.YoutubeClient.GetVideo(e.Message.Content)
	if err != nil {
		return err
	}

	formats := video.Formats.WithAudioChannels() // only get videos with audio
	stream, _, err := b.YoutubeClient.GetStream(video, &formats[0])
	if err != nil {
		return err
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
		return err
	}
	err = ffmpeg.Start()
	if err != nil {
		return err
	}

	session, err := voice.NewSession(b.Ctx.State)
	if err != nil {
		return err
	}

	voiceState, err := b.Ctx.State.VoiceState(e.GuildID, e.Author.ID)
	if err != nil {
		return err
	}

	userChannelID := voiceState.ChannelID
	err = session.JoinChannel(e.GuildID, userChannelID, false, true)
	if err != nil {
		return err
	}
	defer session.Leave()

	udp := session.VoiceUDPConn()
	udp.ResetFrequency(60*time.Millisecond, 2880)

	if err := session.Speaking(voicegateway.Microphone); err != nil {
		return errors.Wrap(err, "failed to send speaking")
	}

	if err := oggreader.DecodeBuffered(udp, stdout); err != nil {
		return errors.Wrap(err, "failed to decode ogg")
	}

	if err := ffmpeg.Wait(); err != nil {
		return err
	}

	return nil
}

func (b *Bot) Ping(e *gateway.MessageCreateEvent) (string, error) {
	return "Pong!", nil
}
