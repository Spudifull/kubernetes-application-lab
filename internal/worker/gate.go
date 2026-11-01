package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Gate is shared only with franz-go's rebalance callbacks. A blocked callback
// may run before bind; remember it so the following attempt is canceled too.
type Gate struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	blocked bool
	owned   map[int32]bool
}

func (g *Gate) bind(cancel context.CancelFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancel = cancel
	if g.blocked {
		cancel()
	}
}
func (g *Gate) unbind() { g.mu.Lock(); defer g.mu.Unlock(); g.cancel = nil }
func (g *Gate) Block() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = true
	if g.cancel != nil {
		g.cancel()
	}
}
func (g *Gate) change(partitions map[string][]int32, add bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owned == nil {
		g.owned = map[int32]bool{}
	}
	for _, p := range partitions[jobs.Topic] {
		if add {
			g.owned[p] = true
		} else {
			delete(g.owned, p)
		}
	}
	g.blocked = false
}
func (g *Gate) Ready() bool { g.mu.Lock(); defer g.mu.Unlock(); return len(g.owned) > 0 && !g.blocked }

// Options deliberately select the classic cooperative protocol (not KIP-848).
// Callbacks only signal/log; they never wait for the client or commit offsets.
func Options(g *Gate, logger *slog.Logger) []kgo.Opt {
	callback := func(event string, add bool) func(context.Context, *kgo.Client, map[string][]int32) {
		return func(_ context.Context, _ *kgo.Client, p map[string][]int32) {
			g.change(p, add)
			logger.Info(event, "group", Group, "partitions", p)
		}
	}
	return []kgo.Opt{
		kgo.ConsumerGroup(Group), kgo.ConsumeTopics(jobs.Topic), kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.BlockRebalanceOnPoll(),
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
		kgo.OnPartitionsAssigned(callback("worker_partitions_assigned", true)),
		kgo.OnPartitionsRevoked(callback("worker_partitions_revoked", false)),
		kgo.OnPartitionsLost(callback("worker_partitions_lost", false)),
		kgo.OnPartitionsCallbackBlocked(func(context.Context, *kgo.Client) { g.Block() }),
	}
}
