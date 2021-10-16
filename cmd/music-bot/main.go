package main

import (
	"github.com/diamondburned/arikawa/v3/voice"
	"log"
	"os"

	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/utils/bot"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("error loading .env file")
	}

	b := &Bot{}

	bot.Run(os.Getenv("DISCORD_TOKEN"), b,
		func(ctx *bot.Context) error {
			voice.AddIntents(b.Ctx.State.Gateway)
			ctx.HasPrefix = bot.NewPrefix("!")
			return nil
		},
	)
}

type Bot struct {
	Ctx *bot.Context
}

func (b *Bot) Play(e *gateway.MessageCreateEvent) error {
	session, err := voice.NewSession(b.Ctx.State)
	if err != nil {
		return err
	}

	voiceState, err := b.Ctx.State.VoiceState(e.GuildID, e.Author.ID)
	if err != nil {
		return err
	}

	userChannelId := voiceState.ChannelID

	err = session.JoinChannel(e.GuildID, userChannelId, false, true)
	if err != nil {
		return err
	}
	defer session.Leave()

	select {}
	return nil
}

func (b *Bot) Ping(e *gateway.MessageCreateEvent) (string, error) {
	return "Pong!", nil
}
