package clusterstate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis implements State with go-redis.
// Key contract: docs/scheme2/REDIS_KEYS.md (prefix gbp:).
type Redis struct {
	rdb *redis.Client
}

func NewRedis(addr string) (*Redis, error) {
	if addr == "" {
		return nil, fmt.Errorf("clusterstate: empty redis addr")
	}
	opt, err := redis.ParseURL(addr)
	if err != nil {
		// allow host:port form
		opt = &redis.Options{Addr: addr}
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("clusterstate: redis ping: %w", err)
	}
	return &Redis{rdb: rdb}, nil
}

// NewRedisFromClient wraps an existing go-redis client (tests / custom pools).
// Caller owns Close via Redis.Close.
func NewRedisFromClient(rdb *redis.Client) (*Redis, error) {
	if rdb == nil {
		return nil, fmt.Errorf("clusterstate: nil redis client")
	}
	return &Redis{rdb: rdb}, nil
}

func (r *Redis) Close() error {
	if r == nil || r.rdb == nil {
		return nil
	}
	return r.rdb.Close()
}

func stickyKey(k string) string              { return "gbp:sticky:" + k }
func stickyByAccKey(accountID string) string { return "gbp:sticky:byacc:" + accountID }
func inflightKey(id string) string           { return "gbp:inflight:" + id }
func cooldownKey(id string) string           { return "gbp:cooldown:" + id }
func shardLeaseKey(id int) string            { return fmt.Sprintf("gbp:shard:lease:%d", id) }
func worksetKey(id int) string               { return fmt.Sprintf("gbp:workset:shard:%d", id) }
func refreshDueKey() string                  { return "gbp:refresh:due" }
func refreshLockKey(id string) string        { return "gbp:refresh:lock:" + id }

func (r *Redis) GetSticky(ctx context.Context, stickyKeyStr string) (StickyBinding, error) {
	raw, err := r.rdb.Get(ctx, stickyKey(stickyKeyStr)).Bytes()
	if err == redis.Nil {
		return StickyBinding{}, ErrNotFound
	}
	if err != nil {
		return StickyBinding{}, err
	}
	var b StickyBinding
	if err := json.Unmarshal(raw, &b); err != nil {
		return StickyBinding{}, err
	}
	if b.Exp > 0 && b.Exp < time.Now().Unix() {
		return StickyBinding{}, ErrNotFound
	}
	return b, nil
}

func (r *Redis) PutSticky(ctx context.Context, stickyKeyStr string, b StickyBinding, ttl time.Duration) error {
	if b.Exp == 0 {
		b.Exp = time.Now().Add(ttl).Unix()
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	pipe := r.rdb.TxPipeline()
	pipe.Set(ctx, stickyKey(stickyKeyStr), raw, ttl)
	if b.AccountID != "" {
		pipe.SAdd(ctx, stickyByAccKey(b.AccountID), stickyKeyStr)
		pipe.Expire(ctx, stickyByAccKey(b.AccountID), ttl+time.Hour)
	}
	if b.SecondaryAccountID != "" && b.SecondaryAccountID != b.AccountID {
		pipe.SAdd(ctx, stickyByAccKey(b.SecondaryAccountID), stickyKeyStr)
		pipe.Expire(ctx, stickyByAccKey(b.SecondaryAccountID), ttl+time.Hour)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (r *Redis) ClearSticky(ctx context.Context, stickyKeyStr string) error {
	// best-effort: read binding to drop reverse index
	if b, err := r.GetSticky(ctx, stickyKeyStr); err == nil {
		if b.AccountID != "" {
			_ = r.rdb.SRem(ctx, stickyByAccKey(b.AccountID), stickyKeyStr).Err()
		}
		if b.SecondaryAccountID != "" {
			_ = r.rdb.SRem(ctx, stickyByAccKey(b.SecondaryAccountID), stickyKeyStr).Err()
		}
	}
	return r.rdb.Del(ctx, stickyKey(stickyKeyStr)).Err()
}

func (r *Redis) ClearStickyAccount(ctx context.Context, accountID string) error {
	keys, err := r.rdb.SMembers(ctx, stickyByAccKey(accountID)).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	pipe := r.rdb.TxPipeline()
	for _, k := range keys {
		pipe.Del(ctx, stickyKey(k))
	}
	pipe.Del(ctx, stickyByAccKey(accountID))
	_, err = pipe.Exec(ctx)
	return err
}

func (r *Redis) IncrInflight(ctx context.Context, accountID string, ttl time.Duration) (int64, error) {
	n, err := r.rdb.Incr(ctx, inflightKey(accountID)).Result()
	if err != nil {
		return 0, err
	}
	if ttl > 0 {
		_ = r.rdb.Expire(ctx, inflightKey(accountID), ttl).Err()
	}
	return n, nil
}

func (r *Redis) DecrInflight(ctx context.Context, accountID string) (int64, error) {
	n, err := r.rdb.Decr(ctx, inflightKey(accountID)).Result()
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		_ = r.rdb.Del(ctx, inflightKey(accountID)).Err()
		return 0, nil
	}
	return n, nil
}

func (r *Redis) GetInflight(ctx context.Context, accountID string) (int64, error) {
	n, err := r.rdb.Get(ctx, inflightKey(accountID)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return n, err
}

func (r *Redis) SetCooldown(ctx context.Context, accountID string, until time.Time) error {
	ttl := time.Until(until)
	if ttl <= 0 {
		return r.ClearCooldown(ctx, accountID)
	}
	return r.rdb.Set(ctx, cooldownKey(accountID), until.Unix(), ttl).Err()
}

func (r *Redis) GetCooldown(ctx context.Context, accountID string) (time.Time, bool, error) {
	v, err := r.rdb.Get(ctx, cooldownKey(accountID)).Int64()
	if err == redis.Nil {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if v <= time.Now().Unix() {
		return time.Time{}, false, nil
	}
	return time.Unix(v, 0), true, nil
}

func (r *Redis) ClearCooldown(ctx context.Context, accountID string) error {
	return r.rdb.Del(ctx, cooldownKey(accountID)).Err()
}

func (r *Redis) TryAcquireShard(ctx context.Context, shardID int, workerID string, ttl time.Duration) (ShardLease, bool, error) {
	lease := ShardLease{
		ShardID:  shardID,
		WorkerID: workerID,
		Version:  time.Now().UnixNano(),
		Exp:      time.Now().Add(ttl).Unix(),
	}
	raw, err := json.Marshal(lease)
	if err != nil {
		return ShardLease{}, false, err
	}
	ok, err := r.rdb.SetNX(ctx, shardLeaseKey(shardID), raw, ttl).Result()
	if err != nil {
		return ShardLease{}, false, err
	}
	if ok {
		return lease, true, nil
	}
	// if same owner, treat as renew/reacquire
	cur, err := r.GetShardOwner(ctx, shardID)
	if err == nil && cur.WorkerID == workerID {
		lease.Version = cur.Version + 1
		raw2, _ := json.Marshal(lease)
		if err := r.rdb.Set(ctx, shardLeaseKey(shardID), raw2, ttl).Err(); err != nil {
			return ShardLease{}, false, err
		}
		return lease, true, nil
	}
	if err == nil {
		return cur, false, nil
	}
	return ShardLease{}, false, nil
}

func (r *Redis) RenewShard(ctx context.Context, shardID int, workerID string, ttl time.Duration) (bool, error) {
	cur, err := r.GetShardOwner(ctx, shardID)
	if err != nil {
		return false, nil
	}
	if cur.WorkerID != workerID {
		return false, nil
	}
	cur.Exp = time.Now().Add(ttl).Unix()
	raw, err := json.Marshal(cur)
	if err != nil {
		return false, err
	}
	if err := r.rdb.Set(ctx, shardLeaseKey(shardID), raw, ttl).Err(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Redis) ReleaseShard(ctx context.Context, shardID int, workerID string) error {
	cur, err := r.GetShardOwner(ctx, shardID)
	if err == ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.WorkerID != workerID {
		return nil
	}
	return r.rdb.Del(ctx, shardLeaseKey(shardID)).Err()
}

func (r *Redis) GetShardOwner(ctx context.Context, shardID int) (ShardLease, error) {
	raw, err := r.rdb.Get(ctx, shardLeaseKey(shardID)).Bytes()
	if err == redis.Nil {
		return ShardLease{}, ErrNotFound
	}
	if err != nil {
		return ShardLease{}, err
	}
	var l ShardLease
	if err := json.Unmarshal(raw, &l); err != nil {
		return ShardLease{}, err
	}
	if l.Exp > 0 && l.Exp < time.Now().Unix() {
		return ShardLease{}, ErrNotFound
	}
	return l, nil
}

func (r *Redis) ReplaceWorkset(ctx context.Context, shardID int, accountIDs []string, scores []float64) error {
	key := worksetKey(shardID)
	pipe := r.rdb.TxPipeline()
	pipe.Del(ctx, key)
	if len(accountIDs) > 0 {
		zs := make([]redis.Z, 0, len(accountIDs))
		for i, id := range accountIDs {
			sc := 0.0
			if i < len(scores) {
				sc = scores[i]
			}
			zs = append(zs, redis.Z{Score: sc, Member: id})
		}
		pipe.ZAdd(ctx, key, zs...)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) ListWorkset(ctx context.Context, shardID int, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	// highest score first
	return r.rdb.ZRevRange(ctx, worksetKey(shardID), 0, int64(limit-1)).Result()
}

func (r *Redis) AddRefreshDue(ctx context.Context, accountID string, due time.Time) error {
	return r.rdb.ZAdd(ctx, refreshDueKey(), redis.Z{
		Score:  float64(due.Unix()),
		Member: accountID,
	}).Err()
}

func (r *Redis) ClaimRefreshDue(ctx context.Context, refresherID string, now time.Time, limit int, lockTTL time.Duration) ([]string, error) {
	if limit <= 0 {
		limit = 1
	}
	// fetch due members
	ids, err := r.rdb.ZRangeByScore(ctx, refreshDueKey(), &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(now.Unix(), 10),
		Count: int64(limit * 3), // over-fetch for lock contention
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, limit)
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		ok, err := r.rdb.SetNX(ctx, refreshLockKey(id), refresherID, lockTTL).Result()
		if err != nil || !ok {
			continue
		}
		// remove from due queue after lock
		_ = r.rdb.ZRem(ctx, refreshDueKey(), id).Err()
		out = append(out, id)
	}
	return out, nil
}
