package modules

import (
	"fmt"
	"log"

	"main/config"
	"main/database"
	"main/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

var activeFsub []*utils.ChannelInfo

func Load(c *telegram.Client) error {
	for _, id := range config.FsubIDs {
		info, err := utils.ResolveChannel(c, id)
		if err != nil || info.Link == "" {
			log.Printf("[modules] skipping fsub channel %d: not accessible or no invite link (%v)", id, err)
			continue
		}
		activeFsub = append(activeFsub, info)
	}
	if len(activeFsub) == 0 {
		return fmt.Errorf("no configured fsub channel is accessible (need admin rights / invite link)")
	}

	c.Use(fsubMiddleware)
	RegisterParticipantSync(c)

	for _, info := range activeFsub {
		if _, err := utils.IsMember(c, info.ID, c.Me().ID); err != nil {
			log.Printf("[modules] failed to preload members for %s: %v", info.DisplayName(), err)
		}
	}

	c.On("cmd:start", handleStart)
	c.On("cmd:vote", handleVoteCreate)
	c.On("cmd:votestats", handleVoteStats)
	c.On("cmd:endvote", handleVoteEnd)
	c.On("callback:commands", handleCommandsCallback)
	c.On("callback:back_start", handleBackToStart)
	c.On("callback:endvote_confirm:", handleEndVoteConfirm)
	c.On("callback:endvote_cancel:", handleEndVoteCancel)
	c.On("callback:vote:", handleVoteToggle)

	return nil
}

func findFsubChannel(channelID int64) *utils.ChannelInfo {
	for _, info := range activeFsub {
		if info.ID == channelID {
			return info
		}
	}
	return nil
}

func fsubMiddleware(next telegram.MessageHandler) telegram.MessageHandler {
	return func(m *telegram.NewMessage) error {
		userID := m.SenderID()
		if userID == 0 {
			return next(m)
		}

		var missing []*utils.ChannelInfo
		for _, info := range activeFsub {
			joined, err := utils.IsMember(m.Client, info.ID, userID)
			if err != nil {
				return next(m)
			}
			if !joined {
				missing = append(missing, info)
			}
		}
		if len(missing) == 0 {
			return next(m)
		}

		buttons := make([]telegram.KeyboardButton, 0, len(missing))
		for _, info := range missing {
			buttons = append(buttons, telegram.Button.URL("📢 "+info.DisplayName(), info.Link))
		}
		_, err := m.Reply("🔒 You must join the following channel(s) to use this bot.",
			&telegram.SendOptions{ReplyMarkup: telegram.NewKeyboard().AddRow(buttons...).Build()})
		return err
	}
}

func suspendedCardText(p *database.VoteParticipant) string {
	return activeCardText(p) + "\n\n" +
		"> ⚠️ ᴠᴏᴛɪɴɢ ɪꜱ ᴅɪꜱᴀʙʟᴇᴅ ꜰᴏʀ ᴛʜɪꜱ ᴜꜱᴇʀ ʙᴇᴄᴀᴜꜱᴇ ᴛʜᴇʏ ʟᴇꜰᴛ ᴛʜᴇ ᴄʜᴀɴɴᴇʟ."
}

func activeCardText(p *database.VoteParticipant) string {
	username := p.Username
	if username == "" {
		username = "—"
	} else {
		username = "@" + username
	}
	return fmt.Sprintf(
		"❤‍🔥 PARTICIPANT DETAILS ❤‍🔥\n\n"+
			"‣ ᴜꜱᴇʀ: %s\n"+
			"‣ ᴜꜱᴇʀ-ɪᴅ: %d\n"+
			"‣ ᴜꜱᴇʀɴᴀᴍᴇ: %s\n\n"+
			"ɴᴏᴛᴇ: ᴏɴʟʏ ᴄʜᴀɴɴᴇʟ ꜱᴜʙꜱᴄʀɪʙᴇʀꜱ ᴄᴀɴ ᴠᴏᴛᴇ.\n\n"+
			"×× ᴄʀᴇᴀᴛᴇᴅ ʙʏ - ᴠᴏᴛᴇ ʙᴏᴛ",
		p.FirstName, p.UserID, username,
	)
}

func voteButtonMarkup(voteID, participantID int64, count int) telegram.ReplyMarkup {
	return telegram.NewKeyboard().AddRow(
		telegram.Button.Data(fmt.Sprintf("🗳 Vote • %d", count), fmt.Sprintf("vote:%d:%d", voteID, participantID)),
	).Build()
}
