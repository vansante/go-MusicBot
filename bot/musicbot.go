package bot

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/svenwiltink/go-musicbot/config"
	"github.com/svenwiltink/go-musicbot/player"
	"github.com/svenwiltink/go-musicbot/songplayer"
	"github.com/svenwiltink/go-musicbot/util"
	"github.com/thoj/go-ircevent"
	"strings"
)

type MusicBot struct {
	ircConn   *irc.Connection
	commands  map[string]Command
	whitelist []string
	player    player.MusicPlayer
	config    *config.MusicBot
}

func NewMusicBot(conf *config.MusicBot) (mb *MusicBot, err error) {
	whitelist, err := config.ReadWhitelist(conf.IRC.WhiteListPath)
	if err != nil {
		return
	}

	mb = &MusicBot{
		commands:  make(map[string]Command),
		whitelist: whitelist,
		config:    conf,
	}
	return
}

func (m *MusicBot) SetPlayer(plr player.MusicPlayer) {
	m.player = plr

	m.player.AddListener(player.EVENT_QUEUE_LOADED, func(args ...interface{}) {
		m.Announcef("%sLoaded %d songs from queue file", ITALIC_CHARACTER, len(m.player.GetQueue()))
	})
	m.player.AddListener(player.EVENT_QUEUE_ERROR_LOADING, func(args ...interface{}) {
		m.Announcef("%sError loading queue from file: %v", args[1].(error), INVERSE_CHARACTER)
	})
	m.player.AddListener(player.EVENT_PLAY_START, func(args ...interface{}) {
		if len(args) < 1 {
			return
		}

		itm, ok := args[0].(songplayer.Playable)
		if !ok {
			logrus.Warnf("MusicBot.onPlay: Error casting song: %v", args[0])
			return
		}

		m.ircConn.Actionf(m.config.IRC.Channel, "starts playing: %s", boldText(formatSong(itm)))
	})
	m.player.AddListener(player.EVENT_QUEUE_DONE, func(args ...interface{}) {
		m.ircConn.Action(m.config.IRC.Channel, "finished the queue")
	})
	m.player.AddListener(player.EVENT_ADDED_SONGS_USER, func(args ...interface{}) {
		if len(args) >= 3 {
			user, ok := args[2].(string)
			if !ok || user == "" {
				user = "?"
			}

			songs, ok := args[0].([]songplayer.Playable)
			if ok {
				m.announceAddedSongs(user, songs)
			}
		}
	})
}

func (m *MusicBot) getCommand(name string) (command Command, exists bool) {
	command, exists = m.commands[name]
	return
}

func (m *MusicBot) registerCommand(command Command) bool {
	if _, exists := m.getCommand(command.Name); !exists {
		m.commands[strings.ToLower(command.Name)] = command

		logrus.Debugf("MusicBot.registerCommand: Registered the %s command", command.Name)
		return true
	}
	return false
}

func (m *MusicBot) isUserWhitelisted(realname string) (iswhitelisted bool, index int) {
	for index, name := range m.whitelist {
		if name == realname {
			return true, index
		}
	}
	return false, -1
}

func (m *MusicBot) Start() (err error) {
	m.registerCommand(HelpCommand)
	m.registerCommand(WhitelistCommand)

	m.registerCommand(PlayCommand)
	m.registerCommand(PauseCommand)
	m.registerCommand(NextCommand)
	m.registerCommand(PreviousCommand)
	m.registerCommand(JumpCommand)
	m.registerCommand(SeekCommand)
	m.registerCommand(StopCommand)

	m.registerCommand(CurrentCommand)
	m.registerCommand(ShuffleCommand)
	m.registerCommand(QueueCommand)
	m.registerCommand(HistoryCommand)
	m.registerCommand(FlushCommand)

	m.registerCommand(AddCommand)
	m.registerCommand(OpenCommand)
	m.registerCommand(InsertCommand)
	m.registerCommand(RemoveCommand)

	m.registerCommand(SearchCommand)
	m.registerCommand(SearchAddCommand)

	m.registerCommand(StatsCommand)

	m.registerCommand(VolUpCommand)
	m.registerCommand(VolDownCommand)
	m.registerCommand(VolCommand)

	m.registerCommand(VersionCommand)
	m.registerCommand(LogCommand)

	m.ircConn = irc.IRC(m.config.IRC.Nick, m.config.IRC.Realname)
	m.ircConn.Password = m.config.IRC.Password
	m.ircConn.UseTLS = m.config.IRC.Ssl
	m.ircConn.QuitMessage = "Enjoy your day without music!"

	err = m.ircConn.Connect(m.config.IRC.Server)
	if err != nil {
		logrus.Errorf("MusicBot.Start: Error connecting to IRC server [%s] %v", m.config.IRC.Server, err)
		return
	}

	m.ircConn.AddCallback("001", func(event *irc.Event) {
		event.Connection.Join(m.config.IRC.Channel)
	})
	m.ircConn.AddCallback("PRIVMSG", func(event *irc.Event) {
		channel := event.Arguments[0]
		message := event.Arguments[len(event.Arguments)-1]
		realname := event.User

		if strings.HasPrefix(message, "!music") {
			isWhiteListed, _ := m.isUserWhitelisted(realname)

			if m.player == nil {
				event.Connection.Privmsgf(channel, "%sError: MusicPlayer has not been configured", INVERSE_CHARACTER)
				return
			}

			if m.config.IRC.Master == realname || isWhiteListed {
				arguments := strings.Split(message, " ")[1:]
				if len(arguments) > 0 {
					commandName := strings.ToLower(arguments[0])
					arguments = arguments[1:]
					if command, exist := m.getCommand(commandName); exist {
						command.execute(m, event, arguments)
						return
					}
				}
				event.Connection.Privmsg(channel, "Unknown command. Use !music help to list all the commands available")
				return
			}
			// Unauthorised user
			event.Connection.Privmsgf(channel, "I will not obey you, %s", realname)
		}
	})

	m.ircConn.Privmsgf(m.config.IRC.Channel, "%s %s connected", GetMusicBotStringFormatted(), util.VersionTag)
	return
}

func (m *MusicBot) Stop() (err error) {
	m.ircConn.Action(m.config.IRC.Channel, "quits. Please come back later for more music!")

	err = m.player.Stop()
	return
}

func (m *MusicBot) getTarget(event *irc.Event) (target string, isPrivate, isMain bool) {
	if len(event.Arguments) == 0 {
		return
	}
	target = event.Arguments[0]
	if !strings.HasPrefix(target, "#") {
		target = event.Nick
		isPrivate = true
	}
	isMain = target == m.config.IRC.Channel
	return
}

func (m *MusicBot) announceAddedSongs(user string, songs []songplayer.Playable) {
	var songTitles []string
	i := 6
	for _, song := range songs {
		songTitles = append(songTitles, formatSong(song))
		i--
		if i < 0 {
			songTitles = append(songTitles, italicText(fmt.Sprintf("and %d more..", len(songs)-6)))
			break
		}
	}
	m.ircConn.Privmsgf(m.config.IRC.Channel, "%s added song(s): %s", boldText(user), strings.Join(songTitles, " | "))
}

func (m *MusicBot) Announce(message string) {
	m.ircConn.Privmsg(m.config.IRC.Channel, message)
}

func (m *MusicBot) Announcef(format string, a ...interface{}) {
	m.ircConn.Privmsgf(m.config.IRC.Channel, format, a...)
}

func (m *MusicBot) announceMessage(nonMainOnly bool, event *irc.Event, message string) {
	target, isPrivate, isMain := m.getTarget(event)
	if isPrivate || !nonMainOnly {
		event.Connection.Privmsg(target, message)
	}
	if isPrivate || (!isMain && !nonMainOnly) {
		// Announce it to the main channel as well
		event.Connection.Privmsg(m.config.IRC.Channel, message)
	}
}

func (m *MusicBot) announceMessagef(nonMainOnly bool, event *irc.Event, format string, a ...interface{}) {
	target, isPrivate, isMain := m.getTarget(event)
	if isPrivate || !nonMainOnly {
		event.Connection.Privmsgf(target, format, a...)
	}
	if isPrivate || (!isMain && !nonMainOnly) {
		// Announce it to the main channel as well
		event.Connection.Privmsgf(m.config.IRC.Channel, format, a...)
	}
}
