package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const providerLeaseTTL = 60 * time.Second
const admissionPoll = 250 * time.Millisecond

// Queue and running leases use Redis time. FIFO admission and all key quota
// increments execute in one script, so a waiter cannot consume RPM or a slot.
var providerAdmitScript = redis.NewScript(`
local clock=redis.call('TIME'); local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local queue,live,running,seq=KEYS[1],KEYS[2],KEYS[3],KEYS[4]
local token,cap,ttl=ARGV[1],tonumber(ARGV[2]),tonumber(ARGV[3])
for _,dead in ipairs(redis.call('ZRANGEBYSCORE',live,'-inf',now)) do redis.call('ZREM',queue,dead) end
redis.call('ZREMRANGEBYSCORE',live,'-inf',now)
redis.call('ZREMRANGEBYSCORE',running,'-inf',now)
if not redis.call('ZSCORE',queue,token) then redis.call('ZADD',queue,redis.call('INCR',seq),token) end
redis.call('ZADD',live,now+ttl,token)
redis.call('PEXPIRE',queue,ttl*2);redis.call('PEXPIRE',live,ttl*2);redis.call('PEXPIRE',seq,ttl*2)
local rank=redis.call('ZRANK',queue,token)
if rank>0 then return {-1,250,rank+1} end
if cap>0 and redis.call('ZCARD',running)>=cap then return {-2,250,1} end
if ARGV[4]~='' and redis.call('EXISTS',ARGV[4])==1 then return {-4,250,1} end
local allKeys,allArgs=KEYS,ARGV
local KEYS={};for i=5,#allKeys do table.insert(KEYS,allKeys[i]) end
local ARGV={now};for i=5,#allArgs do table.insert(ARGV,allArgs[i]) end
` + strings.Replace(reserveLua, "return {1, 0}", `
redis.call('ZREM',queue,token);redis.call('ZREM',live,token)
redis.call('ZADD',running,now+ttl,token);redis.call('PEXPIRE',running,ttl*2)
redis.call('PUBLISH',queue..':wake','admitted')
return {1,0,now}`, 1))

var renewProviderLease = redis.NewScript(`
local clock=redis.call('TIME');local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local untilTime=redis.call('ZSCORE',KEYS[1],ARGV[1])
if not untilTime or tonumber(untilTime)<=now then return 0 end
redis.call('ZADD',KEYS[1],now+tonumber(ARGV[2]),ARGV[1]);redis.call('PEXPIRE',KEYS[1],tonumber(ARGV[2])*2);return 1
`)

var touchProviderQueue = redis.NewScript(`
local clock=redis.call('TIME');local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
for _,dead in ipairs(redis.call('ZRANGEBYSCORE',KEYS[2],'-inf',now)) do redis.call('ZREM',KEYS[1],dead) end
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',now)
if not redis.call('ZSCORE',KEYS[1],ARGV[1]) then redis.call('ZADD',KEYS[1],redis.call('INCR',KEYS[4]),ARGV[1]) end
redis.call('ZADD',KEYS[2],now+tonumber(ARGV[2]),ARGV[1])
redis.call('PEXPIRE',KEYS[1],tonumber(ARGV[2])*2);redis.call('PEXPIRE',KEYS[2],tonumber(ARGV[2])*2);redis.call('PEXPIRE',KEYS[4],tonumber(ARGV[2])*2)
return redis.call('ZRANK',KEYS[1],ARGV[1])+1
`)

func providerAdmissionKeys(id string) []string {
	prefix := "provider:admission:" + id
	return []string{prefix + ":queue", prefix + ":waiting", prefix + ":running", prefix + ":sequence"}
}

func (s *Server) removeAdmission(providerID, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	keys := providerAdmissionKeys(providerID)
	p := s.redis.Pipeline()
	for _, key := range keys[:3] {
		p.ZRem(ctx, key, token)
	}
	p.Publish(ctx, keys[0]+":wake", "released")
	_, _ = p.Exec(ctx)
}

type providerLease struct {
	s                 *Server
	providerID, token string
	stop              chan struct{}
	once              sync.Once
	lost              chan struct{}
}

func (l *providerLease) release() {
	if l == nil {
		return
	}
	l.once.Do(func() { close(l.stop); l.s.removeAdmission(l.providerID, l.token) })
}
func (l *providerLease) heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			ok, err := renewProviderLease.Run(ctx, l.s.redis, []string{providerAdmissionKeys(l.providerID)[2]}, l.token, providerLeaseTTL.Milliseconds()).Int()
			cancel()
			if err != nil || ok != 1 {
				close(l.lost)
				return
			}
		}
	}
}

func (s *Server) tryProviderAdmission(ctx context.Context, provider Provider, token string, constraints []limitConstraint, tokenCost int64, cooldownKeys ...string) (reservation, string, int64, time.Duration, error) {
	keys := providerAdmissionKeys(provider.ID)
	cooldownKey := ""
	if len(cooldownKeys) > 0 {
		cooldownKey = cooldownKeys[0]
	}
	args := []any{token, provider.MaxConcurrentRequests, providerLeaseTTL.Milliseconds(), cooldownKey}
	for _, c := range constraints {
		if c.Cost > c.Capacity {
			return reservation{}, "request_exceeds_limit", 0, 0, nil
		}
		keys = append(keys, c.Key)
		args = append(args, c.Capacity, c.WindowMS, c.Cost)
	}
	raw, err := providerAdmitScript.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		return reservation{}, "", 0, 0, err
	}
	number := func(i int) int64 { n, _ := strconv.ParseInt(fmt.Sprint(raw[i]), 10, 64); return n }
	code := number(0)
	retry := time.Duration(number(1)) * time.Millisecond
	if code == 1 {
		lease := &providerLease{s: s, providerID: provider.ID, token: token, stop: make(chan struct{}), lost: make(chan struct{})}
		go lease.heartbeat()
		return reservation{lease: lease, constraints: constraints, tokenCost: tokenCost, reservedAt: number(2)}, "", 0, 0, nil
	}
	if code == -1 {
		return reservation{}, "queue_order", number(2), retry, nil
	}
	if code == -2 {
		return reservation{}, "concurrency_limit", 1, retry, nil
	}
	return reservation{}, "rate_limit", 1, retry, nil
}

// Refresh provider state, route eligibility and newly added/changed keys while
// waiting. Retain the request's explicit fallback tiers and repair preference.
func (s *Server) refreshAdmissionCandidates(ctx context.Context, candidates []routeCandidate) ([]routeCandidate, error) {
	ids := []string{}
	old := map[string]routeCandidate{}
	for _, c := range candidates {
		if _, ok := old[c.Route.Model.ID]; !ok {
			ids = append(ids, c.Route.Model.ID)
			old[c.Route.Model.ID] = c
		}
	}
	rows, err := s.db.Query(ctx, `SELECT `+routeColumns+` FROM model_routes m JOIN providers p ON p.id=m.provider_id WHERE m.id=ANY($1) AND `+routeFilter+` ORDER BY array_position($1::text[],m.id)`, ids)
	if err != nil {
		return nil, err
	}
	routes := []routeRuntime{}
	for rows.Next() {
		route, e := scanRoute(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		route.FallbackOnly = old[route.Model.ID].Route.FallbackOnly
		route.Provider.AlternativeProviderID = old[route.Model.ID].Route.Provider.AlternativeProviderID
		routes = append(routes, route)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	fresh, err := s.loadPoolCandidates(ctx, routes)
	if err != nil {
		return nil, err
	}
	for i := range fresh {
		for _, c := range candidates {
			if c.key() == fresh[i].key() {
				fresh[i].repairPreferred = c.repairPreferred
			}
		}
	}
	return fresh, nil
}

func (s *Server) selectQueuedCandidate(ctx context.Context, req dispatchRequest, candidates []routeCandidate, costs map[string]int64, skipped map[string]bool, forced string, waitingProvider ...string) (*routeCandidate, reservation, []RoutingDecision, error) {
	token, err := newID("queue")
	if err != nil {
		return nil, reservation{}, nil, err
	}
	providerID := ""
	if len(waitingProvider) > 0 {
		providerID = waitingProvider[0]
	}
	defer func() {
		if providerID != "" {
			s.removeAdmission(providerID, token)
		}
	}()
	cursor, err := s.redis.Incr(ctx, "rr:pool:"+req.Alias).Result()
	if err != nil {
		return nil, reservation{}, nil, err
	}
	var subscription *redis.PubSub
	defer func() {
		if subscription != nil {
			_ = subscription.Close()
		}
	}()
	var wake <-chan *redis.Message
	if providerID != "" {
		subscription = s.redis.Subscribe(ctx, providerAdmissionKeys(providerID)[0]+":wake")
		wake = subscription.Channel()
	}
	var lastRefresh time.Time
	queuedAt := time.Now()
	// Keep the original route set so a temporarily unavailable route can be
	// refreshed without losing the configured alternatives and key additions.
	originalCandidates := candidates
	for {
		if err := ctx.Err(); err != nil {
			return nil, reservation{}, nil, err
		}
		if time.Since(lastRefresh) >= time.Second {
			fresh, e := s.refreshAdmissionCandidates(ctx, originalCandidates)
			if e != nil {
				return nil, reservation{}, nil, e
			}
			candidates = filterForcedCredential(fresh, forced)
			lastRefresh = time.Now()
		}
		decisions := []RoutingDecision{}
		waiting := false
		reason := "rate_limit"
		position := int64(1)
		for _, index := range candidateSelectionOrder(candidates, cursor) {
			c := candidates[index]
			if _, ok := costs[c.Route.Model.ID]; !ok || skipped[c.key()] {
				continue
			}
			if providerID != "" && providerID != c.Route.Provider.ID {
				continue
			}
			credential := c.Credential
			if !credential.Enabled || credential.Status == "quarantined" {
				decisions = append(decisions, RoutingDecision{CredentialID: credential.ID, Reason: "unavailable"})
				continue
			}
			if decision := balanceRoutingDecision(credential); decision != nil {
				decisions = append(decisions, *decision)
				continue
			}
			hold, e := s.redis.Get(ctx, "cooldown:"+credential.ID).Result()
			if e != nil && !errors.Is(e, redis.Nil) {
				return nil, reservation{}, decisions, e
			}
			if hold != "" && hold != "429" {
				decisions = append(decisions, RoutingDecision{CredentialID: credential.ID, Reason: "unavailable"})
				continue
			}
			constraints, rejected := buildConstraintsWithCosts(credential, c.Route.Model.ID, 1, costs[c.Route.Model.ID], costs[c.Route.Model.ID])
			impossible := len(rejected) > 0
			for _, limit := range constraints {
				if limit.Cost > limit.Capacity {
					impossible = true
				}
			}
			if impossible {
				decisions = append(decisions, RoutingDecision{CredentialID: credential.ID, Reason: "request_exceeds_limit"})
				continue
			}
			if providerID == "" {
				providerID = c.Route.Provider.ID
				subscription = s.redis.Subscribe(ctx, providerAdmissionKeys(providerID)[0]+":wake")
				wake = subscription.Channel()
			}
			position, e = touchProviderQueue.Run(ctx, s.redis, providerAdmissionKeys(providerID), token, providerLeaseTTL.Milliseconds()).Int64()
			if e != nil {
				return nil, reservation{}, decisions, e
			}
			if hold == "429" {
				waiting = true
				reason = "rate_limit"
				continue
			}
			reserved, why, rank, _, e := s.tryProviderAdmission(ctx, c.Route.Provider, token, constraints, costs[c.Route.Model.ID], "cooldown:"+credential.ID)
			if e != nil {
				return nil, reservation{}, decisions, e
			}
			if why == "" {
				s.updateActiveRequest(req.RequestID, func(log *RequestLog) {
					log.State = "running"
					log.Running = true
					log.WaitReason = ""
					log.QueuePosition = 0
					log.QueueWaitMS += time.Since(queuedAt).Milliseconds()
					log.QueuedAt = nil
				})
				providerID = "" // successful admission is now owned by the returned lease
				return &c, reserved, decisions, nil
			}
			waiting = true
			reason = why
			position = rank
		}
		if !waiting {
			if providerID != "" {
				s.removeAdmission(providerID, token)
				providerID = ""
				if subscription != nil {
					_ = subscription.Close()
					subscription = nil
					wake = nil
				}
				continue
			}
			return nil, reservation{}, decisions, nil
		}
		// Touch FIFO membership even when every key is cooling down; otherwise a
		// request arriving later could overtake this waiter after the reset.
		s.updateActiveRequest(req.RequestID, func(log *RequestLog) {
			log.State = "queued"
			log.QueuedAt = &queuedAt
			log.Running = false
			log.WaitReason = reason
			log.QueuePosition = position
			for _, c := range candidates {
				if c.Route.Provider.ID == providerID {
					log.ProviderName = c.Route.Provider.Name
					break
				}
			}
		})
		timer := time.NewTimer(admissionPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, reservation{}, decisions, ctx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
