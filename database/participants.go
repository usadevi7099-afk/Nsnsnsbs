package database

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type VoteParticipant struct {
	VoteID    int64     `bson:"vote_id"`
	UserID    int64     `bson:"user_id"`
	Username  string    `bson:"username,omitempty"`
	FirstName string    `bson:"first_name"`
	JoinedAt  time.Time `bson:"joined_at"`
	Suspended bool      `bson:"suspended,omitempty"`
	VotedFor  int64     `bson:"voted_for,omitempty"`
	VoteCount int       `bson:"vote_count,omitempty"`
	VoterIDs  []int64   `bson:"voter_ids,omitempty"`

	MsgID int32 `bson:"msg_id,omitempty"`
}

type VoteAction string

const (
	VoteCast    VoteAction = "cast"
	VoteRemoved VoteAction = "removed"
	VoteBlocked VoteAction = "blocked"
)

type voteCacheState struct {
	mu sync.Mutex

	voterChoices map[string]int64

	targetVoters map[string]map[int64]struct{}

	hydratedVoters  map[string]struct{}
	hydratedTargets map[string]struct{}

	dirtyVoters  map[string]struct{}
	dirtyTargets map[string]struct{}
}

var liveVotes = voteCacheState{
	voterChoices:    make(map[string]int64),
	targetVoters:    make(map[string]map[int64]struct{}),
	hydratedVoters:  make(map[string]struct{}),
	hydratedTargets: make(map[string]struct{}),
	dirtyVoters:     make(map[string]struct{}),
	dirtyTargets:    make(map[string]struct{}),
}

func partKey(voteID, userID int64) string {
	return strconv.FormatInt(voteID, 10) + ":" + strconv.FormatInt(userID, 10)
}

func HasParticipated(voteID, userID int64) (bool, error) {
	p, err := GetParticipant(voteID, userID)
	return p != nil, err
}

func GetParticipant(voteID, userID int64) (*VoteParticipant, error) {
	key := partKey(voteID, userID)
	if v, found := partCache.Get(key); found {
		return v, nil
	}

	c, cancel := ctx()
	defer cancel()

	var p VoteParticipant
	err := partsColl.FindOne(c, bson.M{"vote_id": voteID, "user_id": userID}).Decode(&p)
	if err == mongo.ErrNoDocuments {
		partCache.Set(key, nil)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get participant: %w", err)
	}

	p.VoteCount = normalizedVoteCount(&p)
	partCache.Set(key, &p)
	return &p, nil
}

func normalizedVoteCount(p *VoteParticipant) int {
	if len(p.VoterIDs) > 0 {
		return len(p.VoterIDs)
	}
	return p.VoteCount
}

func AddParticipant(voteID, userID int64, username, firstName string) (bool, error) {
	key := partKey(voteID, userID)

	if v, found := partCache.Get(key); found && v != nil {
		return false, nil
	}

	partMu.Lock()
	defer partMu.Unlock()

	if v, found := partCache.Get(key); found && v != nil {
		return false, nil
	}

	p := VoteParticipant{
		VoteID:    voteID,
		UserID:    userID,
		Username:  username,
		FirstName: firstName,
		JoinedAt:  time.Now(),
		VoterIDs:  []int64{},
	}

	c, cancel := ctx()
	defer cancel()

	_, err := partsColl.InsertOne(c, p)
	if mongo.IsDuplicateKeyError(err) {
		partCache.Set(key, &p)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to add participant: %w", err)
	}

	partCache.Set(key, &p)
	return true, nil
}

func RemoveParticipant(voteID, userID int64) error {
	key := partKey(voteID, userID)

	c, cancel := ctx()
	defer cancel()

	_, err := partsColl.DeleteOne(c, bson.M{"vote_id": voteID, "user_id": userID})
	if err != nil {
		return fmt.Errorf("failed to remove participant: %w", err)
	}

	partCache.Set(key, nil)
	liveVotes.mu.Lock()
	delete(liveVotes.voterChoices, key)
	delete(liveVotes.targetVoters, key)
	delete(liveVotes.hydratedVoters, key)
	delete(liveVotes.hydratedTargets, key)
	delete(liveVotes.dirtyVoters, key)
	delete(liveVotes.dirtyTargets, key)
	liveVotes.mu.Unlock()
	return nil
}

func SetParticipantMsgID(voteID, userID int64, msgID int32) error {
	return updateParticipantField(voteID, userID, "msg_id", msgID)
}

func SuspendParticipant(voteID, userID int64) error {
	return updateParticipantField(voteID, userID, "suspended", true)
}

func ResumeParticipant(voteID, userID int64) error {
	return updateParticipantField(voteID, userID, "suspended", false)
}

func updateParticipantField(voteID, userID int64, field string, value any) error {
	key := partKey(voteID, userID)

	c, cancel := ctx()
	defer cancel()

	_, err := partsColl.UpdateOne(c,
		bson.M{"vote_id": voteID, "user_id": userID},
		bson.M{"$set": bson.M{field: value}},
	)
	if err != nil {
		return fmt.Errorf("failed to update participant %s: %w", field, err)
	}

	if v, found := partCache.Get(key); found && v != nil {
		switch field {
		case "msg_id":
			v.MsgID = value.(int32)
		case "suspended":
			v.Suspended = value.(bool)
		}
	}
	return nil
}

func ToggleVote(voteID, voterID, targetID int64) (VoteAction, int64, int, error) {
	liveVotes.mu.Lock()
	defer liveVotes.mu.Unlock()

	if err := hydrateVoterChoiceLocked(voteID, voterID); err != nil {
		return "", 0, 0, err
	}
	if err := hydrateTargetVotersLocked(voteID, targetID); err != nil {
		return "", 0, 0, err
	}

	voterKey := partKey(voteID, voterID)
	targetKey := partKey(voteID, targetID)
	currentVote := liveVotes.voterChoices[voterKey]

	if currentVote == targetID {
		delete(liveVotes.voterChoices, voterKey)
		delete(liveVotes.targetVoters[targetKey], voterID)
		markDirtyLocked(voterKey, targetKey)
		count := len(liveVotes.targetVoters[targetKey])
		updateCachedVoteCount(voteID, targetID, count)
		updateCachedVotedFor(voteID, voterID, 0)
		return VoteRemoved, 0, count, nil
	}

	if currentVote != 0 {
		return VoteBlocked, currentVote, len(liveVotes.targetVoters[targetKey]), nil
	}

	if liveVotes.targetVoters[targetKey] == nil {
		liveVotes.targetVoters[targetKey] = make(map[int64]struct{})
	}
	liveVotes.voterChoices[voterKey] = targetID
	liveVotes.targetVoters[targetKey][voterID] = struct{}{}
	markDirtyLocked(voterKey, targetKey)
	count := len(liveVotes.targetVoters[targetKey])
	updateCachedVoteCount(voteID, targetID, count)
	updateCachedVotedFor(voteID, voterID, targetID)
	return VoteCast, 0, count, nil
}

func hydrateVoterChoiceLocked(voteID, voterID int64) error {
	key := partKey(voteID, voterID)
	if _, ok := liveVotes.hydratedVoters[key]; ok {
		return nil
	}
	p, err := GetParticipant(voteID, voterID)
	if err != nil {
		return err
	}
	if p != nil && p.VotedFor != 0 {
		liveVotes.voterChoices[key] = p.VotedFor
	}
	liveVotes.hydratedVoters[key] = struct{}{}
	return nil
}

func hydrateTargetVotersLocked(voteID, targetID int64) error {
	key := partKey(voteID, targetID)
	if _, ok := liveVotes.hydratedTargets[key]; ok {
		return nil
	}
	p, err := GetParticipant(voteID, targetID)
	if err != nil {
		return err
	}
	voters := make(map[int64]struct{})
	if p != nil {
		for _, voterID := range p.VoterIDs {
			voters[voterID] = struct{}{}
		}
	}
	liveVotes.targetVoters[key] = voters
	liveVotes.hydratedTargets[key] = struct{}{}
	return nil
}

func markDirtyLocked(voterKey, targetKey string) {
	liveVotes.dirtyVoters[voterKey] = struct{}{}
	liveVotes.dirtyTargets[targetKey] = struct{}{}
}

func ClearVotedFor(voteID, voterID int64) (int64, error) {
	liveVotes.mu.Lock()
	defer liveVotes.mu.Unlock()

	if err := hydrateVoterChoiceLocked(voteID, voterID); err != nil {
		return 0, err
	}

	voterKey := partKey(voteID, voterID)
	previousTarget := liveVotes.voterChoices[voterKey]
	if previousTarget == 0 {
		updateCachedVotedFor(voteID, voterID, 0)
		return 0, nil
	}

	targetKey := partKey(voteID, previousTarget)
	if err := hydrateTargetVotersLocked(voteID, previousTarget); err != nil {
		return 0, err
	}
	delete(liveVotes.voterChoices, voterKey)
	delete(liveVotes.targetVoters[targetKey], voterID)
	markDirtyLocked(voterKey, targetKey)
	updateCachedVotedFor(voteID, voterID, 0)
	updateCachedVoteCount(voteID, previousTarget, len(liveVotes.targetVoters[targetKey]))
	return previousTarget, nil
}

func GetVoteCount(voteID, targetID int64) (int, error) {
	liveVotes.mu.Lock()
	defer liveVotes.mu.Unlock()

	if err := hydrateTargetVotersLocked(voteID, targetID); err != nil {
		return 0, err
	}
	return len(liveVotes.targetVoters[partKey(voteID, targetID)]), nil
}

func updateCachedVoteCount(voteID, targetID int64, count int) {
	if v, found := partCache.Get(partKey(voteID, targetID)); found && v != nil {
		v.VoteCount = count
	}
}

func updateCachedVotedFor(voteID, voterID, targetID int64) {
	if v, found := partCache.Get(partKey(voteID, voterID)); found && v != nil {
		v.VotedFor = targetID
	}
}

func FlushVoteCacheOnce() error {
	voters, targets := snapshotDirtyVotes()

	for voterKey, targetID := range voters {
		voteID, voterID, err := parsePartKey(voterKey)
		if err != nil {
			continue
		}
		c, cancel := ctxWithTimeout()
		_, err = partsColl.UpdateOne(c,
			bson.M{"vote_id": voteID, "user_id": voterID},
			bson.M{"$set": bson.M{"voted_for": targetID}},
		)
		cancel()
		if err != nil {
			requeueDirtyVoter(voterKey)
			return fmt.Errorf("failed to flush voter %s: %w", voterKey, err)
		}
		updateCachedVotedFor(voteID, voterID, targetID)
	}

	for targetKey, voterIDs := range targets {
		voteID, targetID, err := parsePartKey(targetKey)
		if err != nil {
			continue
		}
		c, cancel := ctxWithTimeout()
		_, err = partsColl.UpdateOne(c,
			bson.M{"vote_id": voteID, "user_id": targetID},
			bson.M{"$set": bson.M{"voter_ids": voterIDs, "vote_count": len(voterIDs)}},
		)
		cancel()
		if err != nil {
			requeueDirtyTarget(targetKey)
			return fmt.Errorf("failed to flush target %s: %w", targetKey, err)
		}
		if p, found := partCache.Get(targetKey); found && p != nil {
			p.VoterIDs = voterIDs
			p.VoteCount = len(voterIDs)
		}
	}
	return nil
}

func snapshotDirtyVotes() (map[string]int64, map[string][]int64) {
	liveVotes.mu.Lock()
	defer liveVotes.mu.Unlock()

	voters := make(map[string]int64, len(liveVotes.dirtyVoters))
	for key := range liveVotes.dirtyVoters {
		voters[key] = liveVotes.voterChoices[key]
		delete(liveVotes.dirtyVoters, key)
	}

	targets := make(map[string][]int64, len(liveVotes.dirtyTargets))
	for key := range liveVotes.dirtyTargets {
		set := liveVotes.targetVoters[key]
		ids := make([]int64, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		targets[key] = ids
		delete(liveVotes.dirtyTargets, key)
	}
	return voters, targets
}

func requeueDirtyVoter(key string) {
	liveVotes.mu.Lock()
	liveVotes.dirtyVoters[key] = struct{}{}
	liveVotes.mu.Unlock()
}

func requeueDirtyTarget(key string) {
	liveVotes.mu.Lock()
	liveVotes.dirtyTargets[key] = struct{}{}
	liveVotes.mu.Unlock()
}

func parsePartKey(key string) (int64, int64, error) {
	pieces := strings.Split(key, ":")
	if len(pieces) != 2 {
		return 0, 0, fmt.Errorf("invalid participant key %q", key)
	}
	voteID, err := strconv.ParseInt(pieces[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	userID, err := strconv.ParseInt(pieces[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return voteID, userID, nil
}

func StartVoteCacheFlusher(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := FlushVoteCacheOnce(); err != nil {
					log.Printf("[database] vote cache flush failed: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
}

func ctxWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func CountParticipants(voteID int64) (int64, error) {
	c, cancel := ctx()
	defer cancel()

	count, err := partsColl.CountDocuments(c, bson.M{"vote_id": voteID, "suspended": bson.M{"$ne": true}})
	if err != nil {
		return 0, fmt.Errorf("failed to count participants: %w", err)
	}
	return count, nil
}

func GetParticipantsRanked(voteID int64) ([]*VoteParticipant, error) {
	if err := FlushVoteCacheOnce(); err != nil {
		log.Printf("[database] pre-rank vote flush failed: %v", err)
	}

	c, cancel := ctx()
	defer cancel()

	cursor, err := partsColl.Find(c,
		bson.M{"vote_id": voteID, "suspended": bson.M{"$ne": true}},
		options.Find().SetSort(bson.D{{Key: "vote_count", Value: -1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ranked participants: %w", err)
	}
	defer cursor.Close(c)

	var list []*VoteParticipant
	for cursor.Next(c) {
		var p VoteParticipant
		if err := cursor.Decode(&p); err == nil {
			p.VoteCount = normalizedVoteCount(&p)
			list = append(list, &p)
		}
	}
	return list, cursor.Err()
}
