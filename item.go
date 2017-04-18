package ephemerald

import (
	"context"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/boz/ephemerald/lifecycle"
	"github.com/boz/ephemerald/params"
	"github.com/docker/docker/api/types"
)

type poolItemEvent string

const (
	eventPoolItemStart poolItemEvent = "start"
	eventPoolItemReset poolItemEvent = "reset"
	eventPoolItemKill  poolItemEvent = "kill"

	eventPoolItemLiveError  poolItemEvent = "live-error"
	eventPoolItemLive       poolItemEvent = "live"
	eventPoolItemResetError poolItemEvent = "reset-error"
	eventPoolItemReady      poolItemEvent = "ready"
	eventPoolItemReadyError poolItemEvent = "ready-error"
)

type poolItem struct {
	lifecycle lifecycle.Manager
	adapter   Adapter
	container PoolContainer

	events chan poolItemEvent
	joinch chan (chan<- poolEvent)

	// closed when exited
	exited chan bool

	ctx    context.Context
	cancel context.CancelFunc

	wg  sync.WaitGroup
	log logrus.FieldLogger
}

func createPoolItem(log logrus.FieldLogger, adapter Adapter, lifecycle lifecycle.Manager) (PoolItem, error) {
	log = log.WithField("component", "pool-item")

	container, err := createPoolContainer(log, adapter)
	if err != nil {
		log.WithError(err).
			Error("can't create container")
		return nil, err
	}

	log = lcid(log, container.ID())

	ctx, cancel := context.WithCancel(context.Background())

	item := &poolItem{
		lifecycle: lifecycle.ForContainer(container.ID()),
		adapter:   adapter,
		container: container,
		events:    make(chan poolItemEvent),
		joinch:    make(chan (chan<- poolEvent)),
		exited:    make(chan bool),
		ctx:       ctx,
		cancel:    cancel,
		log:       log,
	}

	go item.run()

	return item, nil
}

func (i *poolItem) ID() string {
	return i.container.ID()
}

func (i *poolItem) Status() types.ContainerJSON {
	return i.container.Status()
}

func (i *poolItem) Join(ch chan<- poolEvent) {
	i.joinch <- ch
}

func (i *poolItem) Start() {
	go i.sendEvent(eventPoolItemStart)
}

func (i *poolItem) Reset() {
	go i.sendEvent(eventPoolItemReset)
}

func (i *poolItem) Kill() {
	go i.sendEvent(eventPoolItemKill)
}

func (i *poolItem) sendEvent(e poolItemEvent) {
	select {
	case <-i.exited:
	case i.events <- e:
	}
}

func (i *poolItem) run() {
	ch := i.runWaitJoin()
	i.runMainLoop(ch)

	i.drain()
}

func (i *poolItem) runWaitJoin() chan<- poolEvent {
	defer close(i.joinch)

	log := i.log.WithField("method", "runWaitJoin")

	for {
		select {
		case ch := <-i.joinch:
			log.Debug("joined")
			return ch
		case e := <-i.events:
			log.WithField("event", e).Debug("item-event")
			switch e {
			case eventPoolItemKill:
				i.container.Stop()
			}
		}
	}
}

func (i *poolItem) runMainLoop(ch chan<- poolEvent) {
	defer close(i.exited)
	defer i.cancel()

	log := i.log.WithField("method", "runMainLoop")

	for {
		select {
		case e := <-i.container.Events():
			log.WithField("event", e).Debug("container-event")

			switch e {
			case containerEventExitSuccess:
				fallthrough
			case containerEventExitError:
				fallthrough
			case containerEventStartFailed:
				i.log.Info("container exited")
				ch <- poolEvent{eventItemExit, i}
				return
			case containerEventStarted:
				i.do(i.onChildStarted)
			}

		case e := <-i.events:
			log.WithField("event", e).Debug("item-event")

			switch e {
			case eventPoolItemKill:
				i.container.Stop()
			case eventPoolItemStart:
				i.container.Start()
			case eventPoolItemLive:
				i.do(i.onChildLive)
			case eventPoolItemLiveError:
				i.container.Stop()
			case eventPoolItemReady:
				ch <- poolEvent{eventItemReady, i}
			case eventPoolItemReadyError:
				i.container.Stop()
			case eventPoolItemReset:
				i.do(i.onChildReset)
			}

		}
	}
}

func (i *poolItem) drain() {
	log := i.log.WithField("method", "drain")

	defer close(i.events)

	ch := make(chan bool)
	go func() {
		i.wg.Wait()
		close(ch)
	}()

	for {
		select {
		case <-ch:
			log.Debug("done")
			return
		case e := <-i.events:
			log.WithField("event", e).Debug("stale item-event")
		}
	}
}

func (i *poolItem) onChildStarted() {
	if i.lifecycle.HasHealthcheck() {
		params, err := i.currentParams()
		if err != nil {
			i.events <- eventPoolItemLiveError
			return
		}
		if err := i.lifecycle.DoHealthcheck(i.ctx, params); err != nil {
			i.log.WithError(err).Error("error checking liveliness")
			i.events <- eventPoolItemLiveError
			return
		}
	}
	i.events <- eventPoolItemLive
}

func (i *poolItem) onChildLive() {
	if i.lifecycle.HasInitialize() {
		params, err := i.currentParams()
		if err != nil {
			i.events <- eventPoolItemReadyError
			return
		}
		if err := i.lifecycle.DoInitialize(i.ctx, params); err != nil {
			i.log.WithError(err).Error("error initializing")
			i.events <- eventPoolItemReadyError
			return
		}
	}
	i.log.Info("container ready")
	i.events <- eventPoolItemReady
}

func (i *poolItem) onChildReset() {
	if i.lifecycle.HasReset() {
		params, err := i.currentParams()
		if err != nil {
			i.events <- eventPoolItemResetError
			return
		}
		if err := i.lifecycle.DoReset(i.ctx, params); err != nil {
			i.log.WithError(err).Error("error provisioning")
			i.events <- eventPoolItemResetError
			return
		}
		i.events <- eventPoolItemReady
		return
	}
	i.container.Stop()
}

func (i *poolItem) currentParams() (params.Params, error) {
	params, err := i.adapter.MakeParams(i.container)
	if err != nil {
		i.log.WithError(err).Warn("error making params")
	}
	return params, err
}

func (i *poolItem) do(fn func()) {
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		fn()
	}()
}
