package p2p

import (
	"context"
	"io"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type envelopMsgTyp uint8

const (
	envelopMsgNil = envelopMsgTyp(iota)
	envelopMsgAddHandler
	envelopMsgDelHandler
	envelopMsgError
	envelopMsgPacket
	envelopMsgStartSync
	envelopMsgSyncSuccess
)

type envelopMsg struct {
	typ     envelopMsgTyp
	Sender  *Peer
	Packet  *Packet
	handler Handler
	err     error
}

func newEnvelopMsgWithError(sender *Peer, err error) envelopMsg {
	return envelopMsg{
		Sender: sender,
		err:    err,
		typ:    envelopMsgError,
	}
}

func newEnvelopMsg(sender *Peer, packet *Packet) envelopMsg {
	return envelopMsg{
		Sender: sender,
		Packet: packet,
		typ:    envelopMsgPacket,
	}
}

func newHandlerAddMsg(h Handler) envelopMsg {
	return envelopMsg{
		handler: h,
		typ:     envelopMsgAddHandler,
	}
}

func newHandlerDelMsg(h Handler) envelopMsg {
	return envelopMsg{
		handler: h,
		typ:     envelopMsgDelHandler,
	}
}

// peerLoop all packet from peers will process by there
func (c *Client) peerLoop(ctx context.Context) {
	isStopped := false
	for {
		select {
		case r, ok := <-c.packetChan:
			if !ok {
				c.logger.Info("client peerLoop stop")
				return
			}

			switch r.typ {
			case envelopMsgAddHandler:
				c.onAddHandlerMsg(&r)
			case envelopMsgDelHandler:
				c.onDelHandlerMsg(&r)
			case envelopMsgStartSync:
				c.onStartSyncIrreversible(r.Sender)
			case envelopMsgError:
				if !isStopped {
					c.onPeerErrorMsg(&r)
				}
			case envelopMsgPacket:
				c.onPacketMsg(&r)
			}

		case <-ctx.Done():
			if !isStopped {
				isStopped = true
				c.logger.Info("close p2p client")
				c.closeAllPeer()
				c.logger.Info("all peer is closed, to close client peerLoop")
				close(c.packetChan)
			}
		}
	}
}

func (c *Client) onAddHandlerMsg(r *envelopMsg) {
	handlerName := r.handler.Name()
	c.logger.Info("new handler", zap.String("name", handlerName))
	for idx, h := range c.handlers {
		if h.Name() == handlerName {
			c.logger.Info("replace handler", zap.String("name", handlerName))
			c.handlers[idx] = r.handler
			return
		}
	}
	c.handlers = append(c.handlers, r.handler)
}

func (c *Client) onDelHandlerMsg(r *envelopMsg) {
	handlerName := r.handler.Name()
	c.logger.Info("del handler", zap.String("name", handlerName))
	for idx, h := range c.handlers {
		if h.Name() == handlerName {
			// not change seq with handlers
			for i := idx; i < len(c.handlers)-1; i++ {
				c.handlers[i] = c.handlers[i+1]
			}
			c.handlers = c.handlers[:len(c.handlers)-1]
			return
		}
	}
	c.logger.Warn("no found hander to del", zap.String("name", handlerName))
}

func (c *Client) onPacketMsg(r *envelopMsg) {
	envelope := newEnvelope(r.Sender, r.Packet)
	c.syncHandler.Handle(envelope)
	for _, handle := range c.handlers {
		handle.Handle(envelope)
	}
}

func (c *Client) onPeerErrorMsg(r *envelopMsg) {
	if r.err == nil {
		return
	}

	if errors.Cause(r.err) != io.EOF {
		c.logger.Info("client res error", zap.Error(r.err))
	} else {
		c.logger.Info("conn closed")
		c.peerChan <- peerMsg{
			err:    r.err,
			peer:   r.Sender,
			msgTyp: peerMsgErrPeer,
		}
	}

}

// RegisterHandler reg handler to client
func (c *Client) RegisterHandler(handler Handler) {
	c.packetChan <- newHandlerAddMsg(handler)
}
