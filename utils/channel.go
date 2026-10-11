package utils

import (
	"fmt"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

type ChannelInfo struct {
	ID       int64
	Title    string
	Username string
	Link     string
}

func (i *ChannelInfo) DisplayName() string {
	if i.Username != "" {
		return "@" + i.Username
	}
	return i.Title
}

func (i *ChannelInfo) MessageLink(msgID int32) string {
	if i.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", i.Username, msgID)
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", DenormalizeChatID(i.ID), msgID)
}

var channelCache = NewCache[any, *ChannelInfo](time.Hour)

func ResolveChannel(c *telegram.Client, arg any) (*ChannelInfo, error) {
	if info, found := channelCache.Get(arg); found {
		return info, nil
	}

	peer, err := c.ResolvePeer(arg)
	if err != nil {
		return nil, err
	}

	chPeer, ok := peer.(*telegram.InputPeerChannel)
	if !ok {
		return nil, fmt.Errorf("not a channel")
	}

	full, err := c.ChannelsGetFullChannel(&telegram.InputChannelObj{
		ChannelID:  chPeer.ChannelID,
		AccessHash: chPeer.AccessHash,
	})
	if err != nil {
		return nil, err
	}

	channelFull, ok := full.FullChat.(*telegram.ChannelFull)
	if !ok {
		return nil, fmt.Errorf("unexpected full chat type: %T", full.FullChat)
	}

	info := &ChannelInfo{ID: NormalizeChatID(chPeer.ChannelID)}
	for _, ch := range full.Chats {
		if channel, ok := ch.(*telegram.Channel); ok && channel.ID == channelFull.ID {
			info.Title = channel.Title
			info.Username = channel.Username
			break
		}
	}

	if info.Username != "" {
		info.Link = "https://t.me/" + info.Username
	} else if inv, ok := channelFull.ExportedInvite.(*telegram.ChatInviteExported); ok {
		info.Link = inv.Link
	}

	channelCache.Set(arg, info)
	channelCache.Set(info.ID, info)
	return info, nil
}
