package p2p

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// peerMsg for peer new/delete/error msg
type peerMsg struct {
	msgTyp peerMsgTyp
	peer   *Peer
	cfg    *PeerCfg
	err    error
}

type peerMsgTyp uint8

const (
	peerMsgNewPeer = peerMsgTyp(iota)
	peerMsgDelPeer
	peerMsgErrPeer
	peerSyncFinished
)

func (c *Client) peerMngLoop(ctx context.Context) {
	for {
		select {
		case p := <-c.peerChan:
			// if already stop loop so close directly
			select {
			case <-ctx.Done():
				c.logger.Info("close peer chan mng")
				return
			default:
			}

			switch p.msgTyp {
			case peerMsgNewPeer:
				c.onNewPeer(ctx, &p)
			case peerMsgDelPeer:
				c.onDelPeer(ctx, &p)
			case peerMsgErrPeer:
				c.onErrPeer(ctx, &p)
			case peerSyncFinished:
				c.onSyncFinished(ctx, &p)
			}

		case <-ctx.Done():
			// no need wait all msg in chan processed
			c.logger.Info("close peer chan mng")
			return
		}
	}
}

func (c *Client) onNewPeer(ctx context.Context, msg *peerMsg) {
	c.logger.Info("new peer", zap.String("addr", msg.cfg.Address))
	_, ok := c.ps[msg.cfg.Address]
	if ok {
		c.logger.Info("connect had created, no new another", zap.String("addr", msg.cfg.Address))
		return
	}

	peer, err := NewPeer(msg.cfg, c, c.HeadBlockNum(), c.ChainID())

	if err != nil {
		c.logger.Error("new peer failed", zap.String("addr", msg.peer.Address), zap.Error(err))
	}

	c.ps[msg.cfg.Address] = &peerStatus{
		peer:   peer,
		status: peerStatInit,
		cfg:    msg.cfg,
	}

	c.StartPeer(ctx, peer)
}

func (c *Client) onDelPeer(ctx context.Context, msg *peerMsg) {
	ps, ok := c.ps[msg.cfg.Address]
	if !ok {
		c.logger.Error("no status for peer found", zap.String("peer", msg.cfg.Address))
		return // no process
	}

	c.logger.Info("del peer", zap.String("addr", msg.cfg.Address))

	ps.status = peerStatClosed
	ps.peer.ClosePeer()
	ps.peer.Wait()

	c.logger.Info("peer closed", zap.String("addr", msg.cfg.Address))
}

func (c *Client) onErrPeer(ctx context.Context, msg *peerMsg) {
	if msg.err == nil || msg.peer == nil {
		return
	}

	ps, ok := c.ps[msg.peer.Address]
	if !ok {
		c.logger.Error("no status for peer found", zap.String("peer", msg.peer.Address))
		return // no process
	}

	if ps.status == peerStatClosed {
		// had Closed no reconned
		return
	}

	c.logger.Info("reconnect peer", zap.String("addr", msg.peer.Address))
	if err := c.StartPeer(ctx, msg.peer); err != nil {
		time.Sleep(3 * time.Second)
	}

}

// StartPeer start a peer r/w
func (c *Client) StartPeer(ctx context.Context, p *Peer) error {
	c.logger.Info("Start Connect Peer", zap.String("peer", p.Address))

	ps, ok := c.ps[p.Address]
	if !ok {
		c.logger.Error("no status for peer found", zap.String("peer", p.Address))
		return nil // no process
	}

	err := p.Start(ctx)
	if err != nil {
		c.peerChan <- peerMsg{
			err:    errors.Wrap(err, "connect error"),
			peer:   p,
			msgTyp: peerMsgErrPeer,
		}
		return err
	}

	ps.status = peerStatNormal

	// use first peer connected to sync
	if c.needSync && c.currentSyncPeer == nil {
		c.startSyncIrr(p)
		c.currentSyncPeer = p
	}

	return nil
}

// NewPeer new peer to connect
func (c *Client) NewPeer(cfg *PeerCfg) error {
	c.peerChan <- peerMsg{
		msgTyp: peerMsgNewPeer,
		cfg:    cfg,
	}
	return nil
}

// DelPeerByAddress delete peer and close
func (c *Client) DelPeerByAddress(address string) error {
	c.peerChan <- peerMsg{
		msgTyp: peerMsgDelPeer,
		cfg: &PeerCfg{
			Address: address,
		},
	}
	return nil
}
