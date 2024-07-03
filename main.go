package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	gdq "github.com/daenney/gdq/v2"
	cron "github.com/robfig/cron/v3"
)

var (
	token     = flag.String("discord_token", "", "The token of the bot you're running this with.")
	botID     = flag.String("bot_id", "", "The user ID of the bot account.")
	channelID = flag.String("channel_id", "", "The ID of the channel you'd like the bot to update.")
	delay     = flag.Duration("delay", 30*time.Minute, "Post the next event when it is at least this duration away.")
	timezone  = flag.String("timezone", "America/Chicago", "The timezone to post events relative to.")
	gdqEvent  = flag.String("gdq_event_name", "", "The event name of the GDQ event you'd like to track, such as 'sgdq2024'.")
	// TODO: Remove this once the event has been merged upstream.
	SGDQ2024 = gdq.Event{ID: 48, Short: "SGDQ2024", Name: "Summer Games Done Quick", Year: 2024}
)

type client struct {
	sess        *discordgo.Session
	sched       *gdq.Schedule
	tz          *time.Location
	lastMessage *discordgo.Message
}

func (c *client) msgChannel() error {
	nr := c.sched.NextRun()
	starting := time.Until(nr.Start).Round(time.Minute)
	start := nr.Start.In(c.tz)
	msg := fmt.Sprintf("%q by %s starting in %v (%v) with an estimated duration of %v.", nr.Title, nr.Runners.String(), starting, start, nr.Estimate.Duration)

	m, err := c.sess.ChannelMessageSend(*channelID, msg)
	if err != nil {
		return err
	}

	log.Printf("Sent message with ID %v: %q", m.ID, m.Content)
	return nil
}

func (c *client) shouldPost() error {
	nr := c.sched.NextRun()
	starting := time.Until(nr.Start).Round(time.Minute)

	// Wait until duration before the event before posting it.
	if starting > *delay {
		return fmt.Errorf("waiting until next event is %v away, currently %v away at %v", delay, starting, nr.Start)
	}

	lastMsg, err := c.latestMessage()
	if err != nil {
		return err
	}
	log.Printf("Found last message ID %v by %v", lastMsg.ID, lastMsg.Author.Username)

	if strings.Contains(lastMsg.Content, fmt.Sprintf("\"%s\"", nr.Title)) {
		return fmt.Errorf("last message appears to be the same, preventing duplicates; old post: %q; next run: %q", lastMsg.Content, nr.Title)
	}

	return nil
}

func (c *client) maybePostMsg() error {
	if err := c.shouldPost(); err != nil {
		return err
	}

	return c.msgChannel()
}

func (c *client) latestMessage() (*discordgo.Message, error) {
	afterID := ""
	if c.lastMessage != nil {
		afterID = c.lastMessage.ID
	}
	var lastMessage *discordgo.Message

	for {
		messages, err := c.sess.ChannelMessages(*channelID, 100 /*limit*/, "" /*beforeID*/, afterID, "" /*aroundID*/)
		if err != nil {
			return nil, fmt.Errorf("could not retrive messages from channel ID %s starting at ID %s: %v", *channelID, afterID, err)
		}

		if len(messages) == 0 {
			break
		}

		for _, message := range messages {
			afterID = message.ID
			if message.Author.ID != *botID {
				continue
			}
			lastMessage = message
		}
	}

	if c.lastMessage != lastMessage {
		if lastMessage == nil {
			return c.lastMessage, nil
		}

		c.lastMessage = lastMessage
	}

	return lastMessage, nil
}

func main() {
	flag.Parse()
	ctx := context.Background()

	if *token == "" {
		fmt.Fprintln(os.Stderr, "no API token provided, pass it with --token")
		os.Exit(1)
	}

	if *channelID == "" {
		fmt.Fprintln(os.Stderr, "no channel ID provided, pass it with --channel_id")
		os.Exit(1)
	}

	if *botID == "" {
		fmt.Fprintln(os.Stderr, "no bot ID provided, pass it with --bot_id")
		os.Exit(1)
	}

	event := &SGDQ2024
	if *gdqEvent != "" {
		e, ok := gdq.GetEventByName(*gdqEvent)
		if !ok {
			fmt.Fprintf(os.Stderr, "couldn't find event with name %q", *gdqEvent)
			os.Exit(1)
		}
		event = e
	}

	dg, err := discordgo.New("Bot " + *token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create discord client: %v", err)
		os.Exit(1)
	}
	defer dg.Close()

	g := gdq.New(ctx, &http.Client{})
	sched, err := g.Schedule(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create GDQ client: %v", err)
		os.Exit(1)
	}

	cdt, err := time.LoadLocation(*timezone)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load timezone %q: %v", *timezone, err)
		os.Exit(1)
	}

	c := client{
		sess:  dg,
		sched: sched,
		tz:    cdt,
	}

	cr := cron.New()
	cr.AddFunc("@every 1m", func() {
		if err := c.maybePostMsg(); err != nil {
			log.Printf("%v", err)
		}
	})
	log.Print("Started GDQ discord bot")
	cr.Run()
}
