package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const chatLogPollInterval = 250 * time.Millisecond

type chatLogFollower struct {
	path        string
	initialized bool
	missing     bool
	info        os.FileInfo
	offset      int64
	partial     []byte
}

func (f *chatLogFollower) readNewChats() ([]string, error) {
	file, err := os.Open(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !f.initialized {
				f.missing = true
			}
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !f.initialized {
		f.initialized = true
		f.info = info
		if !f.missing {
			f.offset = info.Size()
			return nil, nil
		}
	}
	if f.info == nil || !os.SameFile(f.info, info) || info.Size() < f.offset {
		f.offset = 0
		f.partial = nil
	}
	f.info = info

	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, 4<<20))
	if err != nil {
		return nil, err
	}
	f.offset += int64(len(data))
	if len(data) == 0 {
		return nil, nil
	}

	data = append(f.partial, data...)
	parts := bytes.Split(data, []byte{'\n'})
	f.partial = append(f.partial[:0], parts[len(parts)-1]...)

	chats := make([]string, 0)
	for _, part := range parts[:len(parts)-1] {
		line := strings.TrimSuffix(string(part), "\r")
		if chat, ok := parseMordhauChatLogLine(line); ok {
			chats = append(chats, chat)
		}
	}
	return chats, nil
}

func parseMordhauChatLogLine(line string) (string, bool) {
	const marker = "LogGameMode: Display: "
	markerIndex := strings.Index(line, marker)
	if markerIndex < 0 {
		return "", false
	}
	payload := strings.TrimSpace(line[markerIndex+len(marker):])
	channelEnd := strings.Index(payload, ") ")
	if !strings.HasPrefix(payload, "(") || channelEnd < 2 {
		return "", false
	}
	channel := payload[:channelEnd+1]
	remainder := payload[channelEnd+2:]

	messageStart := strings.LastIndex(remainder, `: "`)
	if messageStart < 1 || !strings.HasSuffix(remainder, `"`) {
		return "", false
	}
	identity := remainder[:messageStart]
	message := strings.TrimSuffix(remainder[messageStart+3:], `"`)
	identitySeparator := strings.LastIndex(identity, ", ")
	if identitySeparator < 1 || identitySeparator+2 >= len(identity) {
		return "", false
	}
	name := identity[:identitySeparator]
	playerID := identity[identitySeparator+2:]

	chat := fmt.Sprintf(
		"Chat: %s, %s, %s %s",
		playerID,
		name,
		channel,
		message,
	)
	return strings.ToValidUTF8(chat, "�"), true
}

func isRCONChatLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "Chat:")
}

func (m *Manager) chatLogLoop(ctx context.Context) {
	follower := &chatLogFollower{path: gameLogPath}
	ticker := time.NewTicker(chatLogPollInterval)
	defer ticker.Stop()

	for {
		chats, err := follower.readNewChats()
		if err == nil {
			for _, chat := range chats {
				m.addRCONEvent("rcon", chat)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
