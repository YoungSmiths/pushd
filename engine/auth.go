package engine

import (
	"errors"
	"fmt"

	"github.com/nicholaskh/golib/cache"
	log "github.com/nicholaskh/log4go"
	"github.com/nicholaskh/pushd/db"
	"labix.org/v2/mgo/bson"
)

var (
	tokenPool  *cache.LruCache = cache.NewLruCache(200000) //token => 1
	loginUsers *cache.LruCache = cache.NewLruCache(200000) //username => 1
)

//Auth for client
func authClient(token string) (string, error) {
	if _, exists := tokenPool.Get(token); exists {
		tokenPool.Del(token)
		return fmt.Sprintf("Auth succeed"), nil
	} else {
		return "", errors.New("Client auth fail")
	}
}

func authServer(appId, secretKey string) (string, error) {
	c := db.MgoSession().DB("pushd").C("user")

	var result interface{}
	err := c.Find(bson.M{"appId": "test_app"}).One(&result)
	if err != nil {
		log.Error("Error occured when query mongodb: %s", err.Error())
	}

	key := result.(bson.M)["secretKey"]
	if key == secretKey {
		return fmt.Sprintf("Auth succeed"), nil
	}

	return "", errors.New("Server auth fail")
}
