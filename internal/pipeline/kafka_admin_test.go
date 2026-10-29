//go:build integration && pipeline

package pipeline_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// deleteKafkaState removes the test's topics and consumer group.
func (e *env) deleteKafkaState(t *testing.T) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(e.brokers...))
	if err != nil {
		t.Errorf("kafka admin client: %v", err)
		return
	}
	defer cl.Close()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	groups := kmsg.NewPtrDeleteGroupsRequest()
	groups.Groups = []string{e.group, e.group + "-b"}
	_, _ = groups.RequestWith(cctx, cl)
	topics := kmsg.NewPtrDeleteTopicsRequest()
	for _, name := range []string{e.topic, e.name + ".schema-history", "__debezium-heartbeat." + e.name} {
		topic := kmsg.NewDeleteTopicsRequestTopic()
		topic.Topic = kmsg.StringPtr(name)
		topics.Topics = append(topics.Topics, topic)
		topics.TopicNames = append(topics.TopicNames, name)
	}
	topics.TimeoutMillis = 15000
	_, _ = topics.RequestWith(cctx, cl)
}

// topicEndOffsets returns the end offset of every partition of the change topic.
func (e *env) topicEndOffsets(t *testing.T) map[int32]int64 {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(e.brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	meta := kmsg.NewPtrMetadataRequest()
	mt := kmsg.NewMetadataRequestTopic()
	mt.Topic = kmsg.StringPtr(e.topic)
	meta.Topics = append(meta.Topics, mt)
	mresp, err := meta.RequestWith(ctx, cl)
	if err != nil || len(mresp.Topics) != 1 {
		t.Fatalf("metadata of %s: %v", e.topic, err)
	}
	req := kmsg.NewPtrListOffsetsRequest()
	lt := kmsg.NewListOffsetsRequestTopic()
	lt.Topic = e.topic
	for _, p := range mresp.Topics[0].Partitions {
		lp := kmsg.NewListOffsetsRequestTopicPartition()
		lp.Partition = p.Partition
		lp.Timestamp = -1 // latest
		lt.Partitions = append(lt.Partitions, lp)
	}
	req.Topics = append(req.Topics, lt)
	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[int32]int64)
	for _, topic := range resp.Topics {
		for _, p := range topic.Partitions {
			out[p.Partition] = p.Offset
		}
	}
	return out
}

// committedOffsets returns the consumer group's committed offset per partition.
func (e *env) committedOffsets(t *testing.T) map[int32]int64 {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(e.brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	req := kmsg.NewPtrOffsetFetchRequest()
	req.Group = e.group
	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[int32]int64)
	for _, topic := range resp.Topics {
		if topic.Topic != e.topic {
			continue
		}
		for _, p := range topic.Partitions {
			if p.Offset >= 0 {
				out[p.Partition] = p.Offset
			}
		}
	}
	return out
}

// waitGroupMembers waits until the consumer group is stable with n members, each
// holding an assignment.
func (e *env) waitGroupMembers(t *testing.T, n int) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(e.brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	last := "no response"
	defer func() {
		if t.Failed() {
			t.Logf("last group description: %s", last)
		}
	}()
	waitFor(t, fmt.Sprintf("group with %d members", n), 60*time.Second, func() bool {
		req := kmsg.NewPtrDescribeGroupsRequest()
		req.Groups = []string{e.group}
		resp, err := req.RequestWith(ctx, cl)
		if err != nil || len(resp.Groups) != 1 {
			return false
		}
		g := resp.Groups[0]
		last = fmt.Sprintf("state %s, protocol type %q, %d members", g.State, g.ProtocolType, len(g.Members))
		for _, m := range g.Members {
			last += fmt.Sprintf(", %s assignment %d bytes", m.ClientID, len(m.MemberAssignment))
		}
		if g.State != "Stable" || len(g.Members) != n {
			return false
		}
		for _, m := range g.Members {
			if len(m.MemberAssignment) == 0 {
				return false
			}
		}
		return true
	})
}
