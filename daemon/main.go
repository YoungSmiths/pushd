package main

import (
	"os"
	"syscall"
	"time"

	"github.com/funkygao/golib/signal"
	"github.com/nicholaskh/golib/server"
	log "github.com/nicholaskh/log4go"
	"github.com/nicholaskh/pushd/config"
	"github.com/nicholaskh/pushd/engine"
	"github.com/nicholaskh/pushd/serv"
)

var (
	pushdServ *serv.PushdServ
	s2sServ   *engine.S2sServ
)

func init() {
	parseFlags()

	if options.showVersion {
		server.ShowVersionAndExit()
	}

	server.SetupLogging(options.logFile, options.logLevel, options.crashLogFile)
}

func main() {
	pushdServ = serv.NewPushdServ()
	pushdServ.LoadConfig(options.configFile)
	pushdServ.Launch()
	go server.RunSysStats(time.Now(), time.Duration(options.tick)*time.Second)

	config.PushdConf = new(config.ConfigPushd)
	config.PushdConf.LoadConfig(pushdServ.Conf)
	go pushdServ.LaunchTcpServ(config.PushdConf.TcpListenAddr, pushdServ, config.PushdConf.ConnTimeout)
	go pushdServ.Stats.Start(config.PushdConf.StatsOutputInterval, config.PushdConf.MetricsLogfile)

	engine.Proxy = engine.NewS2sProxy()
	go engine.Proxy.WaitMsg()

	s2sServ = engine.NewS2sServ()
	s2sServ.LaunchProxyServ()

	signal.RegisterSignalHandler(syscall.SIGINT, func(sig os.Signal) {
		shutdown()
	})

	shutdown()
}

func shutdown() {
	pushdServ.StopTcpServ()
	log.Info("Terminated")
	os.Exit(0)
}
