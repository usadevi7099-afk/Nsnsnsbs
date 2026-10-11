package modules

import (
	"fmt"
	"log"
	"time"

	"main/database"
	"main/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

func RegisterParticipantSync(c *telegram.Client) {
	c.OnParticipant(func(p *telegram.ParticipantUpdate) error {
		if p.UserID() == 0 {
			return nil
		}

		chatID := p.ChatID()
		channelID := p.ChannelID()
		if channelID == 0 {
			channelID = chatID
		}
		channelID = utils.NormalizeChatID(channelID)
		userID := p.UserID()

		switch {
		case p.IsJoined():
			utils.SetMemberStatus(c, channelID, userID, true)
			if fsub := findFsubChannel(channelID); fsub != nil {
				go func() {
					time.Sleep(500 * time.Millisecond)
					text := fmt.Sprintf("Thanks for joining %s! ❤️\n\nSend /start again to continue.", fsub.DisplayName())
					if _, err := c.SendMessage(userID, text); err != nil {
						log.Printf("[modules] greet DM skipped for %d: %v", userID, err)
					}
				}()
			}
			go handleChannelRejoin(c, channelID, userID)

		case p.IsLeft(), p.IsKicked(), p.IsBanned():
			utils.SetMemberStatus(c, channelID, userID, false)
			go handleChannelLeave(c, channelID, userID)
		}
		return nil
	})
}

func handleChannelLeave(c *telegram.Client, channelID, userID int64) {
	votes, err := database.GetVotesByChannel(channelID)
	if err != nil || len(votes) == 0 {
		return
	}

	for _, vote := range votes {
		p, err := database.GetParticipant(vote.ID, userID)
		if err != nil {
			continue
		}

		if p != nil && !p.Suspended {
			if err := database.SuspendParticipant(vote.ID, userID); err != nil {
				log.Printf("[modules] suspend participant %d vote %d: %v", userID, vote.ID, err)
				continue
			}

			if p.VotedFor != 0 {
				_, _ = database.ClearVotedFor(vote.ID, userID)
			}

			if p.MsgID != 0 {
				_, _ = c.EditMessage(channelID, p.MsgID, suspendedCardText(p), &telegram.SendOptions{
					ParseMode: "markdown",
				})
			}
		} else {
			voterP, err := database.GetParticipant(vote.ID, userID)
			if err != nil || voterP == nil || voterP.VotedFor == 0 {
				continue
			}
			target, _ := database.GetParticipant(vote.ID, voterP.VotedFor)
			_, _ = database.ClearVotedFor(vote.ID, userID)

			if target != nil && target.MsgID != 0 {
				newCount, _ := database.GetVoteCount(vote.ID, target.UserID)
				_, _ = c.EditMessage(channelID, target.MsgID, activeCardText(target), &telegram.SendOptions{
					ReplyMarkup: voteButtonMarkup(vote.ID, target.UserID, newCount),
				})
			}
		}
	}
}

func handleChannelRejoin(c *telegram.Client, channelID, userID int64) {
	votes, err := database.GetVotesByChannel(channelID)
	if err != nil || len(votes) == 0 {
		return
	}

	for _, vote := range votes {
		p, err := database.GetParticipant(vote.ID, userID)
		if err != nil || p == nil || !p.Suspended {
			continue
		}

		if err := database.ResumeParticipant(vote.ID, userID); err != nil {
			log.Printf("[modules] resume participant %d vote %d: %v", userID, vote.ID, err)
			continue
		}

		if p.MsgID != 0 {
			count, _ := database.GetVoteCount(vote.ID, userID)
			_, _ = c.EditMessage(channelID, p.MsgID, activeCardText(p), &telegram.SendOptions{
				ReplyMarkup: voteButtonMarkup(vote.ID, userID, count),
			})
		}
	}
}
