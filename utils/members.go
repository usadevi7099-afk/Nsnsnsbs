package utils

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"
)

var memberCache = NewCache[int64, []int64](NoExpiry)

func NormalizeChatID(id int64) int64 {
	if id <= 0 {
		return id
	}
	full, err := strconv.ParseInt(fmt.Sprintf("-100%d", id), 10, 64)
	if err != nil {
		return id
	}
	return full
}

func DenormalizeChatID(id int64) int64 {
	if id >= 0 {
		return id
	}
	bare, err := strconv.ParseInt(strings.TrimPrefix(strconv.FormatInt(id, 10), "-100"), 10, 64)
	if err != nil {
		return id
	}
	return bare
}

func loadMembers(c *telegram.Client, chatID int64) error {
	members, _, err := c.GetChatMembers(chatID, &telegram.ParticipantOptions{Limit: -1})
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(members))
	for _, m := range members {
		if m.User == nil {
			continue
		}
		ids = append(ids, m.User.ID)
	}
	memberCache.Set(chatID, ids)
	log.Printf("[utils] loaded %d members for chat %d", len(ids), chatID)
	return nil
}

func IsMember(c *telegram.Client, chatID, userID int64) (bool, error) {
	ids, found := memberCache.Get(chatID)
	if !found {
		if err := loadMembers(c, chatID); err != nil {
			return false, err
		}
		ids, _ = memberCache.Get(chatID)
	}
	return containsInt64(ids, userID), nil
}

func SetMemberStatus(c *telegram.Client, chatID, userID int64, isMember bool) {
	ids, found := memberCache.Get(chatID)
	if !found {
		if err := loadMembers(c, chatID); err != nil {
			log.Printf("[utils] failed to preload members for chat %d: %v", chatID, err)
			ids = nil
		} else {
			ids, _ = memberCache.Get(chatID)
		}
	}

	has := containsInt64(ids, userID)
	switch {
	case isMember && !has:
		memberCache.Set(chatID, append(ids, userID))
	case !isMember && has:
		memberCache.Set(chatID, removeInt64(ids, userID))
	}
}

func containsInt64(ids []int64, v int64) bool {
	for _, id := range ids {
		if id == v {
			return true
		}
	}
	return false
}

func removeInt64(ids []int64, v int64) []int64 {
	out := ids[:0]
	for _, id := range ids {
		if id != v {
			out = append(out, id)
		}
	}
	return out
}
