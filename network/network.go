package network

import (
	"context"
	"fmt"
	"sync"

	libp2p "github.com/libp2p/go-libp2p"
	p2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/hashicorp/go-hclog"
)

type NetworkServer struct {
	host     host.Host
	logger   hclog.Logger
	peers    map[p2ppeer.ID]struct{}
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	protocol string
}

// NewNetworkServer creates a new network server with the given protocol.
func NewNetworkServer(ctx context.Context, logger hclog.Logger, protocol string) (*NetworkServer, error) {
	h, err := libp2p.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	ns := &NetworkServer{
		host:     h,
		logger:   logger,
		peers:    make(map[p2ppeer.ID]struct{}),
		ctx:      ctx,
		cancel:   cancel,
		protocol: protocol,
	}
	h.SetStreamHandler(protocol, ns.handleStream)
	return ns, nil
}

// handleStream processes an incoming stream.
func (ns *NetworkServer) handleStream(s network.Stream) {
	ns.logger.Info("incoming stream", "from", s.Conn().RemotePeer().String())
	// Real implementation would read and handle messages here.
	s.Close()
}

// ConnectToPeer connects to a peer given its multiaddress string.
func (ns *NetworkServer) ConnectToPeer(addr string) error {
	info, err := p2ppeer.AddrInfoFromString(addr)
	if err != nil {
		return fmt.Errorf("failed to parse peer address: %w", err)
	}
	if err := ns.host.Connect(ns.ctx, *info); err != nil {
		ns.logger.Error("failed to connect to peer", "addr", addr, "error", err)
		return err
	}
	ns.mu.Lock()
	ns.peers[info.ID] = struct{}{}
	ns.mu.Unlock()
	ns.logger.Info("connected to peer", "addr", addr)
	return nil
}

// BroadcastMessage sends the given message to all connected peers.
func (ns *NetworkServer) BroadcastMessage(message []byte) error {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	for peerID := range ns.peers {
		go ns.sendMessage(peerID, message)
	}
	return nil
}

// sendMessage creates a stream to the given peer and sends the message.
func (ns *NetworkServer) sendMessage(peerID p2ppeer.ID, message []byte) {
	s, err := ns.host.NewStream(ns.ctx, peerID, ns.protocol)
	if err != nil {
		ns.logger.Error("failed to create stream to peer", "peer", peerID.String(), "error", err)
		return
	}
	defer s.Close()
	_, err = s.Write(message)
	if err != nil {
		ns.logger.Error("failed to send message", "peer", peerID.String(), "error", err)
	} else {
		ns.logger.Info("message sent", "peer", peerID.String())
	}
}

// GetPeers returns a slice of connected peer IDs.
func (ns *NetworkServer) GetPeers() []p2ppeer.ID {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	peers := make([]p2ppeer.ID, 0, len(ns.peers))
	for peerID := range ns.peers {
		peers = append(peers, peerID)
	}
	return peers
}

// Shutdown cleanly shuts down the network server.
func (ns *NetworkServer) Shutdown() error {
	ns.cancel()
	if err := ns.host.Close(); err != nil {
		ns.logger.Error("error during host close", "error", err)
		return err
	}
	ns.logger.Info("network server shut down")
	return nil
}
