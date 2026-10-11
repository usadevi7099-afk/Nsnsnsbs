package modules

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"main/config"
	"main/database"
	"main/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

const startCaption = "👋 Welcome to **Vote Bot**!\n\n" +
	"Create polls tied to your channel and let only real subscribers vote.\n\n" +
	"Use /vote to create a new vote."

func handleStart(m *telegram.NewMessage) error {
	arg := strings.TrimSpace(m.Args())
	if voteID, ok := parseVoteDeepLink(arg); ok {
		return handleVoteParticipation(m, voteID)
	}

	opts := &telegram.SendOptions{ParseMode: "markdown", ReplyMarkup: telegram.NewKeyboard().AddRow(
		telegram.Button.URL("💬 Support", config.SupportGroup),
		telegram.Button.URL("📢 Channel", config.SupportChannel),
	).AddRow(
		telegram.Button.Data("⚙️ Commands", "commands"),
	).Build()}
	if config.StartImage != "" {
		opts.Media = config.StartImage
	}
	_, err := m.Reply(startCaption, opts)
	return err
}

func parseVoteDeepLink(arg string) (int64, bool) {
	const prefix = "vote_"
	if !strings.HasPrefix(arg, prefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(arg, prefix), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func handleVoteParticipation(m *telegram.NewMessage, voteID int64) error {
	vote, err := database.GetVote(voteID)
	if err != nil {
		return err
	}
	if vote == nil {
		_, err := m.Reply("❌ This vote has already ended or the ID is invalid.")
		return err
	}

	sender, err := m.GetSender()
	if err != nil {
		return err
	}

	joined, err := utils.IsMember(m.Client, vote.ChannelID, sender.ID)
	if err != nil {
		return err
	}
	if !joined {
		return replyMustJoin(m, vote)
	}

	existing, err := database.GetParticipant(voteID, sender.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		if !existing.Suspended {
			_, err := m.Reply(withCardLink(m.Client, "✅ You've already participated in this vote.", vote, existing.MsgID),
				&telegram.SendOptions{ParseMode: "markdown"})
			return err
		}

		participant, mErr := m.Client.GetChatMember(vote.ChannelID, sender.ID)
		if mErr != nil || participant == nil {
			utils.SetMemberStatus(m.Client, vote.ChannelID, sender.ID, false)
			return replyMustJoin(m, vote)
		}
		utils.SetMemberStatus(m.Client, vote.ChannelID, sender.ID, true)

		if err := database.ResumeParticipant(voteID, sender.ID); err != nil {
			return err
		}
		if existing.MsgID != 0 {
			count, _ := database.GetVoteCount(voteID, sender.ID)
			_, _ = m.Client.EditMessage(vote.ChannelID, existing.MsgID, activeCardText(existing), &telegram.SendOptions{
				ReplyMarkup: voteButtonMarkup(voteID, sender.ID, count),
			})
		}
		_, err = m.Reply(withCardLink(m.Client, "✅ Welcome back! Your participation has been re-activated.", vote, existing.MsgID),
			&telegram.SendOptions{ParseMode: "markdown"})
		return err
	}

	added, err := database.AddParticipant(voteID, sender.ID, sender.Username, sender.FirstName)
	if err != nil {
		return err
	}
	if !added {
		var msgID int32
		if p, _ := database.GetParticipant(voteID, sender.ID); p != nil {
			msgID = p.MsgID
		}
		_, err := m.Reply(withCardLink(m.Client, "✅ You've already participated in this vote.", vote, msgID),
			&telegram.SendOptions{ParseMode: "markdown"})
		return err
	}

	utils.SetMemberStatus(m.Client, vote.ChannelID, sender.ID, true)

	p := &database.VoteParticipant{
		VoteID:    voteID,
		UserID:    sender.ID,
		Username:  sender.Username,
		FirstName: sender.FirstName,
	}
	caption := activeCardText(p)
	opts := &telegram.SendOptions{ReplyMarkup: voteButtonMarkup(voteID, sender.ID, 0)}
	if config.VoteImage != "" {
		opts.Media = config.VoteImage
	}

	svcMsg, err := m.Client.SendMessage(vote.ChannelID, caption, opts)
	if err != nil {
		return err
	}

	var msgID int32
	if svcMsg != nil {
		msgID = svcMsg.ID
		if err := database.SetParticipantMsgID(voteID, sender.ID, svcMsg.ID); err != nil {
			log.Printf("[modules] failed to store participant msg id for %d vote %d: %v", sender.ID, voteID, err)
		}
	}

	_, err = m.Reply(withCardLink(m.Client, "✅ You've joined the vote! Your details have been posted to the channel.", vote, msgID),
		&telegram.SendOptions{ParseMode: "markdown"})
	return err
}

func withCardLink(c *telegram.Client, base string, vote *database.Vote, msgID int32) string {
	if msgID == 0 {
		return base
	}
	info, err := utils.ResolveChannel(c, vote.ChannelID)
	if err != nil || info.Link == "" {
		return base
	}
	return base + "\n\n[View your card](" + info.MessageLink(msgID) + ")"
}

func replyMustJoin(m *telegram.NewMessage, vote *database.Vote) error {
	info, err := utils.ResolveChannel(m.Client, vote.ChannelID)
	if err != nil || info.Link == "" {
		mention := mentionUser(m.Client, vote.CreatorID)
		_, err := m.Reply(
			"🔒 "+vote.ChannelName+" is private and I couldn't get an invite link for it.\n\n"+
				"Please contact the vote creator "+mention+" to get an invite.",
			&telegram.SendOptions{ParseMode: "markdown"})
		return err
	}

	markup := telegram.NewKeyboard().AddRow(
		telegram.Button.URL("📢 Join "+vote.ChannelName, info.Link),
	).Build()
	_, err = m.Reply("🔒 Only subscribers of "+vote.ChannelName+" can vote. Join, then send /start again.",
		&telegram.SendOptions{ReplyMarkup: markup})
	return err
}

func mentionUser(c *telegram.Client, userID int64) string {
	u, err := c.GetUser(userID)
	if err != nil || u == nil {
		return fmt.Sprintf("the creator (`%d`)", userID)
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	name := u.FirstName
	if name == "" {
		name = "the creator"
	}
	return fmt.Sprintf("[%s](tg://user?id=%d)", name, userID)
}
