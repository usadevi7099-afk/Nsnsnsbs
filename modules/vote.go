package modules

import (
	"fmt"
	"strconv"
	"strings"

	"main/database"
	"main/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

func handleVoteCreate(m *telegram.NewMessage) error {
	arg := strings.TrimSpace(m.Args())
	if arg == "" {
		_, err := m.Reply("Usage: `/vote @channelusername` or `/vote -100xxxxxxxxxx`",
			&telegram.SendOptions{ParseMode: "markdown"})
		return err
	}

	info, err := utils.ResolveChannel(m.Client, arg)
	if err != nil {
		_, replyErr := m.Reply("❌ Couldn't find that channel: " + err.Error())
		return replyErr
	}
	channelName := info.DisplayName()

	if info.Link == "" {
		_, replyErr := m.Reply("⚠️ I can't get an invite link for " + channelName +
			".\n\nGrant me the **Invite Users via Link** permission (or set a public username), then run /vote again.",
			&telegram.SendOptions{ParseMode: "markdown"})
		return replyErr
	}

	if err := checkBotCanPost(m.Client, info.ID); err != nil {
		msg := "⚠️ I need to be an admin with **Send Messages** permission in " + channelName + ".\n\n" +
			"1. Add me as admin\n2. Grant Post Messages\n3. Run /vote again"
		_, replyErr := m.Reply(msg, &telegram.SendOptions{
			ParseMode: "markdown",
			ReplyMarkup: telegram.NewKeyboard().AddRow(
				telegram.Button.URL("📢 "+channelName, info.Link),
			).Build(),
		})
		return replyErr
	}

	sender, err := m.GetSender()
	if err != nil {
		return err
	}

	vote, err := database.CreateVote(sender.ID, info.ID, channelName)
	if err != nil {
		return err
	}

	link := fmt.Sprintf("https://t.me/%s?start=vote_%d", m.Client.Me().Username, vote.ID)
	markup := telegram.NewKeyboard().AddRow(
		telegram.Button.URL("🗳 Participate", link),
	).Build()

	serviceText := fmt.Sprintf("🗳 **Vote #%d**\n\nOnly subscribers can participate.\nTap below to join!", vote.ID)
	svcMsg, svcErr := m.Client.SendMessage(info.ID, serviceText, &telegram.SendOptions{
		ParseMode:   "markdown",
		ReplyMarkup: markup,
	})
	if svcErr == nil && svcMsg != nil {
		_ = database.SetServiceMsg(vote.ID, svcMsg.ID)
	}

	_, err = m.Reply(fmt.Sprintf(
		"✅ Vote created!\n\n‣ ɪᴅ: `%d`\n‣ ᴄʜᴀɴɴᴇʟ: %s\n\nShare below — only %s subscribers can participate.",
		vote.ID, channelName, channelName,
	), &telegram.SendOptions{ParseMode: "markdown", ReplyMarkup: markup})
	return err
}

func checkBotCanPost(c *telegram.Client, channelID int64) error {
	self := c.Me()
	participant, err := c.GetChatMember(channelID, self.ID)
	if err != nil || participant == nil {
		return fmt.Errorf("bot not in channel")
	}
	switch participant.Status {
	case telegram.Admin:
		if participant.Rights == nil || !participant.Rights.PostMessages {
			return fmt.Errorf("missing post_messages permission")
		}
		return nil
	case telegram.Creator:
		return nil
	default:
		return fmt.Errorf("bot is not admin")
	}
}

func handleVoteStats(m *telegram.NewMessage) error {
	sender, err := m.GetSender()
	if err != nil {
		return err
	}
	voteID, err := resolveVoteID(m.Args(), sender.ID)
	if err != nil {
		_, replyErr := m.Reply(err.Error())
		return replyErr
	}

	vote, err := database.GetVote(voteID)
	if err != nil {
		return err
	}
	if vote == nil {
		_, err := m.Reply("❌ Vote not found or already ended.")
		return err
	}

	count, err := database.CountParticipants(voteID)
	if err != nil {
		return err
	}

	_, err = m.Reply(fmt.Sprintf(
		"📊 Vote `%d`\n\n‣ ᴄʜᴀɴɴᴇʟ: %s\n‣ ᴘᴀʀᴛɪᴄɪᴘᴀɴᴛꜱ: %d",
		vote.ID, vote.ChannelName, count,
	), &telegram.SendOptions{ParseMode: "markdown"})
	return err
}

func handleVoteEnd(m *telegram.NewMessage) error {
	sender, err := m.GetSender()
	if err != nil {
		return err
	}
	voteID, err := resolveVoteID(m.Args(), sender.ID)
	if err != nil {
		_, replyErr := m.Reply(err.Error())
		return replyErr
	}

	vote, err := database.GetVote(voteID)
	if err != nil {
		return err
	}
	if vote == nil {
		_, err := m.Reply("❌ Vote not found or already ended.")
		return err
	}
	if sender.ID != vote.CreatorID {
		_, err := m.Reply("🚫 Only the creator can end this vote.")
		return err
	}

	count, _ := database.CountParticipants(voteID)
	markup := telegram.NewKeyboard().AddRow(
		telegram.Button.Data("✅ Yes, end it", fmt.Sprintf("endvote_confirm:%d", voteID)),
		telegram.Button.Data("❌ Cancel", fmt.Sprintf("endvote_cancel:%d", voteID)),
	).Build()

	_, err = m.Reply(fmt.Sprintf(
		"⚠️ End vote `%d` for %s?\n\nParticipants: **%d**\n\nThis cannot be undone.",
		voteID, vote.ChannelName, count,
	), &telegram.SendOptions{ParseMode: "markdown", ReplyMarkup: markup})
	return err
}

func processEndVoteConfirm(cb *telegram.CallbackQuery) error {
	parts := strings.SplitN(string(cb.Data), ":", 2)
	if len(parts) != 2 {
		_, err := cb.Answer("Invalid data.", &telegram.CallbackOptions{Alert: true})
		return err
	}
	voteID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		_, err := cb.Answer("Invalid vote ID.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	vote, err := database.GetVote(voteID)
	if err != nil || vote == nil {
		_, err := cb.Edit("❌ Vote not found or already ended.")
		return err
	}
	if cb.SenderID != vote.CreatorID {
		_, err := cb.Answer("🚫 Only the creator can end this vote.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	ranked, _ := database.GetParticipantsRanked(voteID)
	count := int64(len(ranked))

	if err := database.FinishVote(voteID); err != nil {
		return err
	}

	resultsBlock := renderResultsBlock(ranked)

	if vote.ServiceMsgID != 0 {
		editText := fmt.Sprintf("🏁 **Vote #%d has ended**\n\nTotal participants: %d\n\n%s",
			voteID, count, resultsBlock)
		_, _ = cb.Client.EditMessage(vote.ChannelID, vote.ServiceMsgID, editText,
			&telegram.SendOptions{ParseMode: "markdown"})
	}

	_, err = cb.Edit(fmt.Sprintf("🏁 Vote `%d` ended with **%d** participant(s).\n\n%s", voteID, count, resultsBlock),
		&telegram.SendOptions{ParseMode: "markdown"})
	return err
}

const resultsCap = 20

func renderResultsBlock(ranked []*database.VoteParticipant) string {
	if len(ranked) == 0 {
		return "> No one participated in this vote."
	}

	shown := ranked
	if len(shown) > resultsCap {
		shown = shown[:resultsCap]
	}

	var b strings.Builder
	b.WriteString("> 🏆 **Results**\n")
	for i, p := range shown {
		profileLink := fmt.Sprintf("tg://user?id=%d", p.UserID)
		b.WriteString(fmt.Sprintf("> %d. [%s](%s) — %d vote(s)\n", i+1, p.FirstName, profileLink, p.VoteCount))
	}
	if len(ranked) > len(shown) {
		b.WriteString(fmt.Sprintf("> …and %d more participant(s)\n", len(ranked)-len(shown)))
	}
	return b.String()
}

func resolveVoteID(arg string, userID int64) (int64, error) {
	arg = strings.TrimSpace(arg)
	if arg != "" {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("❌ Invalid vote ID.")
		}
		return id, nil
	}

	votes, err := database.GetVotesByCreator(userID)
	if err != nil {
		return 0, fmt.Errorf("❌ Couldn't fetch your votes.")
	}
	switch len(votes) {
	case 0:
		return 0, fmt.Errorf("❌ You have no active votes. Create one with /vote.")
	case 1:
		return votes[0].ID, nil
	default:
		lines := "📋 Multiple active votes — specify one:\n\n"
		for _, v := range votes {
			lines += fmt.Sprintf("‣ `%d` — %s\n", v.ID, v.ChannelName)
		}
		return 0, fmt.Errorf("%s", lines)
	}
}
