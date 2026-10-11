package database

import (
	"context"
	"log"
	"sync"
	"time"

	"main/utils"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	client    *mongo.Client
	database  *mongo.Database
	votesColl *mongo.Collection
	partsColl *mongo.Collection
)

var (
	voteCache = utils.NewCache[int64, *Vote](utils.NoExpiry)

	partCache = utils.NewCache[string, *VoteParticipant](utils.NoExpiry)
	partMu    sync.Mutex
)

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func Init(mongoURL string) (func(), error) {
	var err error
	client, err = mongo.Connect(options.Client().ApplyURI(mongoURL))
	if err != nil {
		return nil, err
	}

	database = client.Database("VoteBot")
	votesColl = database.Collection("votes")
	partsColl = database.Collection("vote_participants")

	if err := ensureIndexes(); err != nil {
		return nil, err
	}

	stopFlusher := make(chan struct{})
	StartVoteCacheFlusher(stopFlusher)

	log.Println("[database] connected to MongoDB")

	return func() {
		_ = FlushVoteCacheOnce()
		close(stopFlusher)
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(c); err != nil {
			log.Printf("[database] disconnect error: %v", err)
		}
	}, nil
}

func upsertOptReturnAfter() *options.FindOneAndUpdateOptionsBuilder {
	return options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
}

func ensureIndexes() error {
	c, cancel := ctx()
	defer cancel()

	_, err := partsColl.Indexes().CreateOne(c, mongo.IndexModel{
		Keys:    bson.D{{Key: "vote_id", Value: 1}, {Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}
