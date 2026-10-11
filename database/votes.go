package database

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Vote struct {
	ID           int64     `bson:"_id"`
	CreatorID    int64     `bson:"creator_id"`
	ChannelID    int64     `bson:"channel_id"`
	ChannelName  string    `bson:"channel_name"`
	ServiceMsgID int32     `bson:"service_msg_id,omitempty"`
	CreatedAt    time.Time `bson:"created_at"`
}

func nextVoteID() (int64, error) {
	c, cancel := ctx()
	defer cancel()

	counters := database.Collection("counters")
	var result struct {
		Seq int64 `bson:"seq"`
	}
	err := counters.FindOneAndUpdate(
		c,
		bson.M{"_id": "vote_id"},
		bson.M{"$inc": bson.M{"seq": 1}},
		upsertOptReturnAfter(),
	).Decode(&result)
	if err != nil {
		return 0, err
	}
	return result.Seq, nil
}

func CreateVote(creatorID, channelID int64, channelName string) (*Vote, error) {
	id, err := nextVoteID()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate vote id: %w", err)
	}

	v := &Vote{
		ID:          id,
		CreatorID:   creatorID,
		ChannelID:   channelID,
		ChannelName: channelName,
		CreatedAt:   time.Now(),
	}

	c, cancel := ctx()
	defer cancel()

	_, err = votesColl.InsertOne(c, v)
	if err != nil {
		return nil, fmt.Errorf("failed to create vote: %w", err)
	}

	voteCache.Set(id, v)
	return v, nil
}

func SetServiceMsg(voteID int64, msgID int32) error {
	c, cancel := ctx()
	defer cancel()

	_, err := votesColl.UpdateOne(c,
		bson.M{"_id": voteID},
		bson.M{"$set": bson.M{"service_msg_id": msgID}},
	)
	if err != nil {
		return err
	}
	if v, found := voteCache.Get(voteID); found && v != nil {
		v.ServiceMsgID = msgID
	}
	return nil
}

func GetVote(voteID int64) (*Vote, error) {
	if v, found := voteCache.Get(voteID); found {
		return v, nil
	}

	c, cancel := ctx()
	defer cancel()

	var v Vote
	err := votesColl.FindOne(c, bson.M{"_id": voteID}).Decode(&v)
	if err == mongo.ErrNoDocuments {
		voteCache.Set(voteID, nil)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get vote %d: %w", voteID, err)
	}

	voteCache.Set(voteID, &v)
	return &v, nil
}

func GetVotesByCreator(creatorID int64) ([]*Vote, error) {
	c, cancel := ctx()
	defer cancel()

	cursor, err := votesColl.Find(c, bson.M{"creator_id": creatorID})
	if err != nil {
		return nil, fmt.Errorf("failed to list votes: %w", err)
	}
	defer cursor.Close(c)

	var votes []*Vote
	for cursor.Next(c) {
		var v Vote
		if err := cursor.Decode(&v); err == nil {
			voteCache.Set(v.ID, &v)
			votes = append(votes, &v)
		}
	}
	return votes, cursor.Err()
}

func GetVotesByChannel(channelID int64) ([]*Vote, error) {
	c, cancel := ctx()
	defer cancel()

	cursor, err := votesColl.Find(c, bson.M{"channel_id": channelID})
	if err != nil {
		return nil, fmt.Errorf("failed to list votes for channel: %w", err)
	}
	defer cursor.Close(c)

	var votes []*Vote
	for cursor.Next(c) {
		var v Vote
		if err := cursor.Decode(&v); err == nil {
			voteCache.Set(v.ID, &v)
			votes = append(votes, &v)
		}
	}
	return votes, cursor.Err()
}

func FinishVote(voteID int64) error {
	c, cancel := ctx()
	defer cancel()

	_, err := votesColl.DeleteOne(c, bson.M{"_id": voteID})
	if err != nil {
		return fmt.Errorf("failed to delete vote %d: %w", voteID, err)
	}

	_, err = partsColl.DeleteMany(c, bson.M{"vote_id": voteID})
	if err != nil {
		return fmt.Errorf("failed to delete participants: %w", err)
	}

	voteCache.Set(voteID, nil)
	return nil
}
