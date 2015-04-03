package engine

import (
	"github.com/nicholaskh/golib/server"
	"github.com/nicholaskh/golib/sync2"
	log "github.com/nicholaskh/log4go"
)

const (
	TYPE_CLIENT = 1
	TYPE_SERVER = 2
)

type Client struct {
	Channels map[string]int
	Uname    string
	Type     uint8
	Authed   bool
	Closed   bool
	Mutex    *sync2.Semaphore
	*server.Client
}

func NewClient() (this *Client) {
	this = new(Client)
	this.Channels = make(map[string]int)
	this.Authed = false
	this.Closed = false
	this.Mutex = sync2.NewSemaphore(1, 0)
	return
}

func (this *Client) Close() error {
	log.Debug("client channels: %s", this.Channels)
	log.Debug("pubsub channels: %s", PubsubChannels)

	UnsubscribeAllChannels(this)

	this.Mutex.Acquire()
	// TODO merge
	this.Closed = true
	err := this.Conn.Close()
	this.Mutex.Release()
	return err
}