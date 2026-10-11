package modules

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"main/config"
	"main/database"
	"main/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

var voteTapLimiter = struct {
	sync.Mutex
	lastTap map[int64]time.Time
}{lastTap: make(map[int64]time.Time)}

const minVoteTapInterval = 500 * time.Millisecond

func handleCommandsCallback(cb *telegram.CallbackQuery) error {
	text := "⚙️ **Commands**\n\n" +
		"‣ /vote @channel — create a new vote\n" +
		"‣ /votestats [id] — view participant count\n" +
		"‣ /endvote [id] — finish a vote (creator only)"

	markup := telegram.NewKeyboard().AddRow(
		telegram.Button.Data("◀️ Back", "back_start"),
	).Build()

	_, err := cb.Edit(text, &telegram.SendOptions{ParseMode: "markdown", ReplyMarkup: markup})
	return err
}

func handleBackToStart(cb *telegram.CallbackQuery) error {
	_, err := cb.Edit(startCaption, &telegram.SendOptions{ParseMode: "markdown", ReplyMarkup: telegram.NewKeyboard().AddRow(
		telegram.Button.URL("💬 Support", config.SupportGroup),
		telegram.Button.URL("📢 Channel", config.SupportChannel),
	).AddRow(
		telegram.Button.Data("⚙️ Commands", "commands"),
	).Build()})
	if err == nil {
		_, _ = cb.Answer("Back to start menu.")
	}
	return err
}

func handleEndVoteConfirm(cb *telegram.CallbackQuery) error {
	return processEndVoteConfirm(cb)
}

func handleEndVoteCancel(cb *telegram.CallbackQuery) error {
	_, err := cb.Edit("❌ Cancelled.")
	return err
}

func handleVoteToggle(cb *telegram.CallbackQuery) error {
	parts := strings.SplitN(string(cb.Data), ":", 3)
	if len(parts) != 3 {
		_, err := cb.Answer("Invalid data.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	voteID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		_, err := cb.Answer("Invalid vote ID.", &telegram.CallbackOptions{Alert: true})
		return err
	}
	targetID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		_, err := cb.Answer("Invalid target ID.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	voterID := cb.SenderID

	voteTapLimiter.Lock()
	last, found := voteTapLimiter.lastTap[voterID]
	flooded := found && time.Since(last) < minVoteTapInterval
	if !flooded {
		voteTapLimiter.lastTap[voterID] = time.Now()
	}
	voteTapLimiter.Unlock()
	if flooded {
		_, err := cb.Answer("⚠️ Please don't flood the vote button. Try again in half a second.", &telegram.CallbackOptions{Alert: false})
		return err
	}

	vote, err := database.GetVote(voteID)
	if err != nil || vote == nil {
		_, err := cb.Answer("❌ This vote has ended.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	isMem, err := utils.IsMember(cb.Client, vote.ChannelID, voterID)
	if err != nil || !isMem {
		_, err := cb.Answer("🔒 You must be a subscriber of the channel to vote.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	voter, err := database.GetParticipant(voteID, voterID)
	if err == nil && voter != nil && voter.Suspended {
		_, err := cb.Answer("🔒 You must be a subscriber of the channel to vote.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	target, err := database.GetParticipant(voteID, targetID)
	if err != nil || target == nil {
		_, err := cb.Answer("❌ Participant not found.", &telegram.CallbackOptions{Alert: true})
		return err
	}
	if target.Suspended {
		_, err := cb.Answer("⚠️ This participant has left the channel and cannot receive votes.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	action, blockedOn, count, err := database.ToggleVote(voteID, voterID, targetID)
	if err != nil {
		_, err := cb.Answer("❌ Something went wrong, try again.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	switch action {
	case database.VoteBlocked:
		prev, _ := database.GetParticipant(voteID, blockedOn)
		prevName := "someone"
		if prev != nil {
			prevName = prev.FirstName
		}
		_, err = cb.Answer(fmt.Sprintf("⚠️ You already voted for %s. Tap their button to remove your vote first.", prevName),
			&telegram.CallbackOptions{Alert: true})
		return err

	case database.VoteRemoved:
		if target.MsgID != 0 {
			_, _ = cb.Client.EditMessage(vote.ChannelID, target.MsgID, activeCardText(target), &telegram.SendOptions{
				ReplyMarkup: voteButtonMarkup(voteID, targetID, count),
			})
		}
		_, err = cb.Answer(fmt.Sprintf("🗳 Vote removed. %s now has %d vote(s).", target.FirstName, count),
			&telegram.CallbackOptions{Alert: false})
		return err

	default:
		if target.MsgID != 0 {
			_, _ = cb.Client.EditMessage(vote.ChannelID, target.MsgID, activeCardText(target), &telegram.SendOptions{
				ReplyMarkup: voteButtonMarkup(voteID, targetID, count),
			})
		}
		_, err = cb.Answer(fmt.Sprintf("✅ Voted for %s! They now have %d vote(s).", target.FirstName, count),
			&telegram.CallbackOptions{Alert: false})
		return err
	}
}
