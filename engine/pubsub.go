package engine

import (
	"fmt"
	"time"

	"github.com/nicholaskh/golib/cache"
	cmap "github.com/nicholaskh/golib/concurrent/map"
	"github.com/nicholaskh/golib/set"
	log "github.com/nicholaskh/log4go"
	"github.com/nicholaskh/pushd/config"
	"github.com/nicholaskh/pushd/engine/storage"
	"strconv"
	"bytes"
	"github.com/nicholaskh/pushd/db"
	"gopkg.in/mgo.v2/bson"
	"encoding/binary"
)

var (
	PubsubChannels *PubsubChans
	UuidToClient *UuidClientMap
)

type PubsubChans struct {
	*cache.LruCache
}

func NewPubsubChannels(maxChannelItems int) (this *PubsubChans) {
	this = new(PubsubChans)
	this.LruCache = cache.NewLruCache(maxChannelItems)
	return
}

func (this *PubsubChans) Get(channel string) (clients cmap.ConcurrentMap, exists bool) {
	clientsInterface, exists := PubsubChannels.LruCache.Get(channel)
	clients, _ = clientsInterface.(cmap.ConcurrentMap)
	return
}

type UuidClientMap struct {
	uuidToClient cmap.ConcurrentMap
}

func NewUuidClientMap() (this *UuidClientMap) {
	this = new(UuidClientMap)
	this.uuidToClient = cmap.New()
	return
}

func (this *UuidClientMap) AddClient(uuid string, client *Client) {
	this.uuidToClient.Set(uuid, client)
}

func (this *UuidClientMap) GetClient(uuid string) (client *Client, exists bool) {
	temp, exists := this.uuidToClient.Get(uuid)
	if exists {
		client = temp.(*Client)
	}
	return
}

func (this *UuidClientMap) Remove(uuid string, client *Client) {
	cli, exists := this.GetClient(uuid)
	if !exists {
		return
	}
	cli.Mutex.Lock()
	defer cli.Mutex.Unlock()

	cli, _ = this.GetClient(uuid)
	if cli != client {
		return
	}
	this.uuidToClient.Remove(uuid)
}

func Subscribe(cli *Client, channel string) string {
	log.Debug("%x", channel)
	_, exists := cli.Channels[channel]
	if exists {
		return fmt.Sprintf("%s %s", OUTPUT_ALREADY_SUBSCRIBED, channel)
	} else {
		cli.Channels[channel] = 1
		clients, exists := PubsubChannels.Get(channel)
		if exists {
			clients.Set(cli.RemoteAddr().String(), cli)
		} else {
			clients = cmap.New()
			clients.Set(cli.RemoteAddr().String(), cli)
			//s2s
			if config.PushdConf.IsDistMode() {
				Proxy.SubMsgChan <- channel
			}
			PubsubChannels.Set(channel, clients)
		}

		return fmt.Sprintf("%s %s", OUTPUT_SUBSCRIBED, channel)
	}

}

func Unsubscribe(cli *Client, channel string) string {
	_, exists := cli.Channels[channel]
	if exists {
		delete(cli.Channels, channel)
		clients, exists := PubsubChannels.Get(channel)
		if exists {
			clients.Remove(cli.RemoteAddr().String())
		}
		clients, exists = PubsubChannels.Get(channel)

		if clients.Count() == 0 {
			PubsubChannels.Del(channel)

			//s2s
			if config.PushdConf.IsDistMode() {
				Proxy.UnsubMsgChan <- channel
			}
		}

		return fmt.Sprintf("%s %s", OUTPUT_UNSUBSCRIBED, channel)
	} else {
		return fmt.Sprintf("%s %s", OUTPUT_NOT_SUBSCRIBED, channel)
	}
}

func UnsubscribeAllChannels(cli *Client) {
	for channel, _ := range cli.Channels {
		clients, _ := PubsubChannels.Get(channel)
		clients.Remove(cli.RemoteAddr().String())
		if clients.Count() == 0 {
			PubsubChannels.Del(channel)

			//s2s
			if config.PushdConf.IsDistMode() {
				Proxy.UnsubMsgChan <- channel
			}
		}
	}
	cli.Channels = nil
}

func Publish(channel, msg , uuid string, msgId int64, fromS2s bool) string {

	clients, exists := PubsubChannels.Get(channel)
	ts := time.Now().UnixNano()
	if exists {
		log.Debug("channel %s subscribed by %d clients", channel, clients.Count())
		for ele := range clients.Iter() {
			cli := ele.Val.(*Client)
			if cli.uuid == uuid {
				continue
			}
			log.Info(fmt.Sprintf("log push: %s -> %s, channle:%s msgId:%d content:%s", uuid, cli.uuid, channel, msgId, msg))
			go cli.PushMsg(OUTPUT_RCIV, fmt.Sprintf("%s %s %d %d %s", channel, uuid, ts, msgId, msg),
				channel, msgId, ts)
		}
	}

	storage.MsgCache.Store(&storage.MsgTuple{Channel: channel, Msg: msg, Ts: ts, Uuid: uuid})
	if !fromS2s && config.PushdConf.EnableStorage() {
		storage.EnqueueMsg(channel, msg, uuid, ts, msgId)
	}

	channelKey := fmt.Sprintf("channel_stat.%s", channel)
	db.MgoSession().DB("pushd").
		C("user_info").
		Update(
		bson.M{"_id": uuid},
		bson.M{"$set": bson.M{channelKey: ts}})

	if !fromS2s {
		//s2s
		if config.PushdConf.IsDistMode() {
			peers, exists := Proxy.Router.LookupPeersByChannel(channel)
			log.Debug("now peers %s", peers)

			if exists {
				Proxy.PubMsgChan <- NewPubTuple(peers, msg, channel, uuid, ts, msgId)
			} else {
				// boradcast to every node
				peers = set.NewSet()
				for _, v := range Proxy.Router.Peers {
					peers.Add(v)
				}
				Proxy.PubMsgChan <- NewPubTuple(peers, msg, channel, uuid, ts, msgId)
			}
		}

		return fmt.Sprintf("%s %d", strconv.FormatInt(msgId, 10), ts);
	} else {
		return ""
	}
}

// 直接推送msg消息，不做任何判断
func Publish2(channel, msg, skipUserId string, forceToOtherNode bool) {
	clients, exists := PubsubChannels.Get(channel)
	if exists {
		for ele := range clients.Iter() {
			cli := ele.Val.(*Client)
			if cli.uuid == skipUserId {
				continue
			}
			go cli.WriteFormatMsg(OUTPUT_RCIV, msg)
		}
	}

	if config.PushdConf.IsDistMode() {
		peers, exists := Proxy.Router.LookupPeersByChannel(channel)
		msg2 := fmt.Sprintf("%s %s %s %s", S2S_PUB_CMD, S2S_PUSH_CMD, channel, msg)
		if exists {
			Proxy.PubMsgChan2 <- NewPubTuple2(peers, msg2)
		} else if forceToOtherNode {
			// boradcast to every node
			peers = set.NewSet()
			for _, v := range Proxy.Router.Peers {
				peers.Add(v)
			}
			Proxy.PubMsgChan2 <- NewPubTuple2(peers, msg2)
		}
	}
}

func Forward(channel, uuid string, msg []byte, fromS2s bool) {
	clients, exists := PubsubChannels.Get(channel)
	if exists {
		// generate binary msg
		data := bytes.NewBuffer([]byte{})

		opBytes := []byte(CMD_VIDO_CHAT)
		// write op to data
		buf := bytes.NewBuffer([]byte{})
		binary.Write(buf, binary.BigEndian, int32(len(opBytes)))
		data.Write(buf.Bytes())

		buf.Reset()
		binary.Write(buf, binary.BigEndian, opBytes)
		data.Write(buf.Bytes())

		// write body to data
		body := bytes.NewBuffer([]byte{})
		body.WriteString(uuid)
		body.WriteByte(' ')
		body.WriteString(channel)
		body.WriteByte(' ')
		body.Write(msg)

		buf.Reset()
		binary.Write(buf, binary.BigEndian, int32(body.Len()))
		data.Write(buf.Bytes())

		buf.Reset()
		binary.Write(buf, binary.BigEndian, body.Bytes())
		data.Write(buf.Bytes())

		resMsg := data.Bytes()

		// send to clients
		for ele := range clients.Iter() {
			cli := ele.Val.(*Client)
			if cli.uuid == uuid {
				continue
			}
			go cli.WriteBinMsg(resMsg)
		}
	}

	//TODO 多节点转发

}

// TODO 整理规范此文件中的所有方法

// 向其他所有服务器发送某条消息
func forwardToAllOtherServer(cmd, message string){
	if !config.PushdConf.IsDistMode() || cmd == "" || message == ""{
		return
	}

	msg := fmt.Sprintf("%s %s", cmd, message)

	peers := set.NewSet()
	for _, v := range Proxy.Router.Peers {
		peers.Add(v)
	}
	Proxy.PubMsgChan2 <- NewPubTuple2(peers, msg)
}
