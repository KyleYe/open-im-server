package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KyleYe/open-im-server/v3/pkg/common/storage/cache"
	"github.com/KyleYe/open-im-server/v3/pkg/common/storage/cache/cachekey"
	"github.com/KyleYe/open-im-server/v3/pkg/common/storage/database"
	"github.com/KyleYe/open-im-server/v3/pkg/msgprocessor"
	"github.com/KyleYe/open-im-tools/errs"
	"github.com/KyleYe/open-im-tools/log"
	"github.com/dtm-labs/rockscache"
	"github.com/redis/go-redis/v9"
)

func NewSeqConversationCacheRedis(rdb redis.UniversalClient, mgo database.SeqConversation) cache.SeqConversationCache {
	return &seqConversationCacheRedis{
		rdb:              rdb,
		mgo:              mgo,
		lockTime:         time.Second * 3,
		dataTime:         time.Hour * 24 * 365,
		minSeqExpireTime: time.Hour,
		rocks:            rockscache.NewClient(rdb, *GetRocksCacheOptions()),
	}
}

type seqConversationCacheRedis struct {
	rdb              redis.UniversalClient
	mgo              database.SeqConversation
	rocks            *rockscache.Client
	lockTime         time.Duration
	dataTime         time.Duration
	minSeqExpireTime time.Duration
}

func (s *seqConversationCacheRedis) getMinSeqKey(conversationID string) string {
	return cachekey.GetMallocMinSeqKey(conversationID)
}

func (s *seqConversationCacheRedis) SetMinSeq(ctx context.Context, conversationID string, seq int64) error {
	return s.SetMinSeqs(ctx, map[string]int64{conversationID: seq})
}

func (s *seqConversationCacheRedis) GetMinSeq(ctx context.Context, conversationID string) (int64, error) {
	return getCache(ctx, s.rocks, s.getMinSeqKey(conversationID), s.minSeqExpireTime, func(ctx context.Context) (int64, error) {
		return s.mgo.GetMinSeq(ctx, conversationID)
	})
}

func (s *seqConversationCacheRedis) getSingleMaxSeq(ctx context.Context, conversationID string) (map[string]int64, error) {
	seq, err := s.GetMaxSeq(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return map[string]int64{conversationID: seq}, nil
}

func (s *seqConversationCacheRedis) batchGetMaxSeq(ctx context.Context, keys []string, keyConversationID map[string]string, seqs map[string]int64) error {
	result := make([]*redis.StringCmd, len(keys))
	pipe := s.rdb.Pipeline()
	for i, key := range keys {
		result[i] = pipe.HGet(ctx, key, "CURR")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return errs.Wrap(err)
	}
	var notFoundKey []string
	for i, r := range result {
		req, err := r.Int64()
		if err == nil {
			seqs[keyConversationID[keys[i]]] = req
		} else if errors.Is(err, redis.Nil) {
			notFoundKey = append(notFoundKey, keys[i])
		} else {
			return errs.Wrap(err)
		}
	}
	for _, key := range notFoundKey {
		conversationID := keyConversationID[key]
		seq, err := s.GetMaxSeq(ctx, conversationID)
		if err != nil {
			return err
		}
		seqs[conversationID] = seq
	}
	return nil
}

func (s *seqConversationCacheRedis) GetMaxSeqs(ctx context.Context, conversationIDs []string) (map[string]int64, error) {
	switch len(conversationIDs) {
	case 0:
		return map[string]int64{}, nil
	case 1:
		return s.getSingleMaxSeq(ctx, conversationIDs[0])
	}
	keys := make([]string, 0, len(conversationIDs))
	keyConversationID := make(map[string]string, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		key := s.getSeqMallocKey(conversationID)
		if _, ok := keyConversationID[key]; ok {
			continue
		}
		keys = append(keys, key)
		keyConversationID[key] = conversationID
	}
	if len(keys) == 1 {
		return s.getSingleMaxSeq(ctx, conversationIDs[0])
	}
	slotKeys, err := groupKeysBySlot(ctx, s.rdb, keys)
	if err != nil {
		return nil, err
	}
	seqs := make(map[string]int64, len(conversationIDs))
	for _, keys := range slotKeys {
		if err := s.batchGetMaxSeq(ctx, keys, keyConversationID, seqs); err != nil {
			return nil, err
		}
	}
	return seqs, nil
}

func (s *seqConversationCacheRedis) getSeqMallocKey(conversationID string) string {
	return cachekey.GetMallocSeqKey(conversationID)
}

func (s *seqConversationCacheRedis) setSeq(ctx context.Context, key string, owner int64, currSeq int64, lastSeq int64) (int64, error) {
	if lastSeq < currSeq {
		return 0, errs.New("lastSeq must be greater than currSeq")
	}
	// 0: success
	// 1: success the lock has expired, but has not been locked by anyone else
	// 2: already locked, but not by yourself
	script := `
local key = KEYS[1]
local lockValue = ARGV[1]
local dataSecond = ARGV[2]
local curr_seq = tonumber(ARGV[3])
local last_seq = tonumber(ARGV[4])
if redis.call("EXISTS", key) == 0 then
	redis.call("HSET", key, "CURR", curr_seq, "LAST", last_seq)
	redis.call("EXPIRE", key, dataSecond)
	return 1
end
if redis.call("HGET", key, "LOCK") ~= lockValue then
	return 2
end
redis.call("HDEL", key, "LOCK")
redis.call("HSET", key, "CURR", curr_seq, "LAST", last_seq)
redis.call("EXPIRE", key, dataSecond)
return 0
`
	result, err := s.rdb.Eval(ctx, script, []string{key}, owner, int64(s.dataTime/time.Second), currSeq, lastSeq).Int64()
	if err != nil {
		return 0, errs.Wrap(err)
	}
	return result, nil
}

// malloc size=0 is to get the current seq size>0 is to allocate seq
func (s *seqConversationCacheRedis) malloc(ctx context.Context, key string, size int64) ([]int64, error) {
	// 0: success
	// 1: need to obtain and lock
	// 2: already locked
	// 3: exceeded the maximum value and locked
	script := `
local key = KEYS[1]
local size = tonumber(ARGV[1])
local lockSecond = ARGV[2]
local dataSecond = ARGV[3]
local result = {}
if redis.call("EXISTS", key) == 0 then
	local lockValue = math.random(0, 999999999)
	redis.call("HSET", key, "LOCK", lockValue)
	redis.call("EXPIRE", key, lockSecond)
	table.insert(result, 1)
	table.insert(result, lockValue)
	return result
end
if redis.call("HEXISTS", key, "LOCK") == 1 then
	table.insert(result, 2)
	return result
end
local curr_seq = tonumber(redis.call("HGET", key, "CURR"))
local last_seq = tonumber(redis.call("HGET", key, "LAST"))
if size == 0 then
	redis.call("EXPIRE", key, dataSecond)
	table.insert(result, 0)
	table.insert(result, curr_seq)
	table.insert(result, last_seq)
	return result
end
local max_seq = curr_seq + size
if max_seq > last_seq then
	local lockValue = math.random(0, 999999999)
	redis.call("HSET", key, "LOCK", lockValue)
	redis.call("HSET", key, "CURR", last_seq)
	redis.call("EXPIRE", key, lockSecond)
	table.insert(result, 3)
	table.insert(result, curr_seq)
	table.insert(result, last_seq)
	table.insert(result, lockValue)
	return result
end
redis.call("HSET", key, "CURR", max_seq)
redis.call("EXPIRE", key, dataSecond)
table.insert(result, 0)
table.insert(result, curr_seq)
table.insert(result, last_seq)
return result
`
	result, err := s.rdb.Eval(ctx, script, []string{key}, size, int64(s.lockTime/time.Second), int64(s.dataTime/time.Second)).Int64Slice()
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return result, nil
}

func (s *seqConversationCacheRedis) wait(ctx context.Context) error {
	timer := time.NewTimer(time.Second / 4)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *seqConversationCacheRedis) setSeqRetry(ctx context.Context, key string, owner int64, currSeq int64, lastSeq int64) {
	for i := 0; i < 10; i++ {
		state, err := s.setSeq(ctx, key, owner, currSeq, lastSeq)
		if err != nil {
			log.ZError(ctx, "set seq cache failed", err, "key", key, "owner", owner, "currSeq", currSeq, "lastSeq", lastSeq, "count", i+1)
			if err := s.wait(ctx); err != nil {
				return
			}
			continue
		}
		switch state {
		case 0: // ideal state
		case 1:
			log.ZWarn(ctx, "set seq cache lock not found", nil, "key", key, "owner", owner, "currSeq", currSeq, "lastSeq", lastSeq)
		case 2:
			log.ZWarn(ctx, "set seq cache lock to be held by someone else", nil, "key", key, "owner", owner, "currSeq", currSeq, "lastSeq", lastSeq)
		default:
			log.ZError(ctx, "set seq cache lock unknown state", nil, "key", key, "owner", owner, "currSeq", currSeq, "lastSeq", lastSeq)
		}
		return
	}
	log.ZError(ctx, "set seq cache retrying still failed", nil, "key", key, "owner", owner, "currSeq", currSeq, "lastSeq", lastSeq)
}

func (s *seqConversationCacheRedis) getMallocSize(conversationID string, size int64) int64 {
	if size == 0 {
		return 0
	}
	var basicSize int64
	if msgprocessor.IsGroupConversationID(conversationID) {
		basicSize = 100
	} else {
		basicSize = 50
	}
	basicSize += size
	return basicSize
}

func (s *seqConversationCacheRedis) Malloc(ctx context.Context, conversationID string, size int64) (int64, error) {
	if size < 0 {
		return 0, errs.New("size must be greater than 0")
	}
	key := s.getSeqMallocKey(conversationID)
	for i := 0; i < 10; i++ {
		states, err := s.malloc(ctx, key, size)
		if err != nil {
			return 0, err
		}
		switch states[0] {
		case 0: // success
			return states[1], nil
		case 1: // not found
			mallocSize := s.getMallocSize(conversationID, size)
			seq, err := s.mgo.Malloc(ctx, conversationID, mallocSize)
			if err != nil {
				return 0, err
			}
			s.setSeqRetry(ctx, key, states[1], seq+size, seq+mallocSize)
			return seq, nil
		case 2: // locked
			if err := s.wait(ctx); err != nil {
				return 0, err
			}
			continue
		case 3: // exceeded cache max value
			currSeq := states[1]
			lastSeq := states[2]
			mallocSize := s.getMallocSize(conversationID, size)
			seq, err := s.mgo.Malloc(ctx, conversationID, mallocSize)
			if err != nil {
				return 0, err
			}
			if lastSeq == seq {
				s.setSeqRetry(ctx, key, states[3], currSeq+size, seq+mallocSize)
				return currSeq, nil
			} else {
				log.ZWarn(ctx, "malloc seq not equal cache last seq", nil, "conversationID", conversationID, "currSeq", currSeq, "lastSeq", lastSeq, "mallocSeq", seq)
				s.setSeqRetry(ctx, key, states[3], seq+size, seq+mallocSize)
				return seq, nil
			}
		default:
			log.ZError(ctx, "malloc seq unknown state", nil, "state", states[0], "conversationID", conversationID, "size", size)
			return 0, errs.New(fmt.Sprintf("unknown state: %d", states[0]))
		}
	}
	log.ZError(ctx, "malloc seq retrying still failed", nil, "conversationID", conversationID, "size", size)
	return 0, errs.New("malloc seq waiting for lock timeout", "conversationID", conversationID, "size", size)
}

func (s *seqConversationCacheRedis) GetMaxSeq(ctx context.Context, conversationID string) (int64, error) {
	return s.Malloc(ctx, conversationID, 0)
}

func (s *seqConversationCacheRedis) SetMinSeqs(ctx context.Context, seqs map[string]int64) error {
	keys := make([]string, 0, len(seqs))
	for conversationID, seq := range seqs {
		keys = append(keys, s.getMinSeqKey(conversationID))
		if err := s.mgo.SetMinSeq(ctx, conversationID, seq); err != nil {
			return err
		}
	}
	return DeleteCacheBySlot(ctx, s.rocks, keys)
}
