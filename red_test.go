package red

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/go-redsync/redsync/v4"
	"github.com/pghq/go-tea"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	tea.SetGlobalLogWriter(io.Discard)
	defer tea.ResetGlobalLogger()
	code := m.Run()
	os.Exit(code)
}

func TestRed(t *testing.T) {
	t.Run("raises queue connection errors", func(t *testing.T) {
		queue, err := NewQueue("")
		assert.NotNil(t, err)
		assert.Nil(t, queue)
	})

	t.Run("can create instance", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		opts := []Option{
			WithRedis(db),
			WithConsumers(1),
			Read(redsync.WithExpiry(time.Second)),
			Write(redsync.WithExpiry(time.Second)),
			Name("red.messages"),
			At(time.Millisecond),
		}
		queue, err := NewQueue("", opts...)
		assert.Nil(t, err)
		assert.NotNil(t, queue)
		assert.Equal(t, 1, len(queue.readOptions))
		assert.Equal(t, 1, len(queue.writeOptions))
	})

	t.Run("can send messages", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(1), MaxMessages(1))
		msg := &Message{}
		msg = queue.Send(msg).Send(msg).Message()
		assert.NotNil(t, msg)
		assert.Nil(t, queue.Message())
	})

	t.Run("can send errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(1), MaxErrors(1))
		err := tea.NewError("an error has occurred")
		err = queue.SendError(err).SendError(err).Error()
		assert.NotNil(t, err)
		assert.Nil(t, queue.Error())
	})

	t.Run("raises consumption errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		queue.consume(&badDelivery{})
		assert.NotNil(t, queue.Error())
	})

	t.Run("can consume deliveries", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		queue.consume(&goodDelivery{})
		assert.NotNil(t, queue.Message())
	})

	t.Run("can decode messages", func(t *testing.T) {
		msg := Message{Id: "test", Value: []byte(`{"key": "value"}`)}
		var value struct {
			Value string `json:"key"`
		}
		err := msg.Decode(&value)
		assert.Nil(t, err)
		assert.Equal(t, "value", value.Value)
	})

	t.Run("message raises ack errors", func(t *testing.T) {
		msg := Message{
			ack: func() error { return tea.NewError("an error has occurred") },
		}

		err := msg.Ack(context.TODO())
		assert.NotNil(t, err)
	})

	t.Run("can ack message", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		msg := Message{
			Id:   "test",
			pool: queue.pool,
			ack:  func() error { return nil },
		}

		err := msg.Ack(context.TODO())
		assert.Nil(t, err)
	})

	t.Run("message raises reject errors", func(t *testing.T) {
		msg := Message{
			reject: func() error { return tea.NewError("an error has occurred") },
		}

		err := msg.Reject(context.TODO())
		assert.NotNil(t, err)
	})

	t.Run("can reject message", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		msg := Message{
			Id:   "test",
			pool: queue.pool,
			ack:  func() error { return nil },
		}

		err := msg.Reject(context.TODO())
		assert.Nil(t, err)
	})

	t.Run("enqueue raises busy lock errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0), Write(redsync.WithTries(1)))

		_ = queue.Enqueue(context.TODO(), "test", "value")
		err := queue.Enqueue(context.TODO(), "test", "value")
		assert.NotNil(t, err)
		assert.False(t, tea.IsFatal(err))
	})

	t.Run("enqueue raises bad value errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		err := queue.Enqueue(context.TODO(), "test", func() {})
		assert.NotNil(t, err)
		assert.False(t, tea.IsFatal(err))
	})

	t.Run("can enqueue", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		err := queue.Enqueue(context.TODO(), "test", "value")
		assert.Nil(t, err)
	})

	t.Run("dequeue raises ctx errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_, err := queue.Dequeue(ctx)
		assert.NotNil(t, err)
	})

	t.Run("dequeue raises empty queue errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		_, err := queue.Dequeue(context.TODO())
		assert.NotNil(t, err)
	})

	t.Run("dequeue handles read lock errors", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))
		mutex := queue.pool.NewMutex("red.r.test")
		mutex.Lock()
		defer mutex.Unlock()

		queue.Send(&Message{Id: "test", reject: func() error { return tea.NewError("an error") }})
		ctx, cancel := context.WithTimeout(context.TODO(), time.Millisecond)
		defer cancel()
		_, err := queue.Dequeue(ctx)
		assert.NotNil(t, err)
	})

	t.Run("can dequeue", func(t *testing.T) {
		db, teardown := setup(t)
		defer teardown()

		queue, _ := NewQueue("", WithRedis(db), WithConsumers(0))

		queue.Send(&Message{Id: "test", reject: func() error {
			return tea.NewError("an error has occurred")
		}})
		m, err := queue.Dequeue(context.TODO())
		assert.Nil(t, err)
		assert.NotNil(t, m)
		assert.Equal(t, "test", m.Id)
	})

}

func setup(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	s, _ := miniredis.Run()
	return redis.NewClient(&redis.Options{Addr: s.Addr()}), s.Close
}

// badDelivery is a partial mock for rmq deliveries with bad json
type badDelivery struct {
	rmq.Delivery
}

func (d badDelivery) Payload() string {
	return ""
}

// goodDelivery is a partial mock for rmq deliveries with good json
type goodDelivery struct {
	rmq.Delivery
}

func (d goodDelivery) Payload() string {
	return "{}"
}
