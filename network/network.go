package network

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/go-hclog"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	p2ppeer "github.com/libp2p/go-libp2p/core/peer"
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
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}

	h, err := libp2p.New()
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
	
	logger.Info("network server created", "protocol", protocol, "peerID", h.ID().String())
	return ns, nil
}

// handleStream processes an incoming stream.
func (ns *NetworkServer) handleStream(s network.Stream) {
	peerID := s.Conn().RemotePeer()
	ns.logger.Info("incoming stream", "from", peerID.String())

	// Add peer to the peers map
	ns.mu.Lock()
	ns.peers[peerID] = struct{}{}
	ns.mu.Unlock()

	// In a real implementation, you would read and process messages here
	// For now, we just close the stream
	defer s.Close()
}

// ConnectToPeer connects to a peer given its multiaddress string.
func (ns *NetworkServer) ConnectToPeer(addr string) error {
	info, err := p2ppeer.AddrInfoFromString(addr)
	if err != nil {
		return fmt.Errorf("failed to parse peer address: %w", err)
	}

	if err := ns.host.Connect(ns.ctx, *info); err != nil {
		ns.logger.Error("failed to connect to peer", "addr", addr, "error", err)
		return fmt.Errorf("failed to connect to peer %s: %w", addr, err)
	}

	ns.mu.Lock()
	ns.peers[info.ID] = struct{}{}
	ns.mu.Unlock()

	ns.logger.Info("connected to peer", "addr", addr, "peerID", info.ID.String())
	return nil
}

// BroadcastMessage sends the given message to all connected peers.
func (ns *NetworkServer) BroadcastMessage(message []byte) error {
	if len(message) == 0 {
		return fmt.Errorf("cannot broadcast empty message")
	}

	ns.mu.RLock()
	peers := make([]p2ppeer.ID, 0, len(ns.peers))
	for peerID := range ns.peers {
		peers = append(peers, peerID)
	}
	ns.mu.RUnlock()

	for _, peerID := range peers {
		go ns.sendMessage(peerID, message)
	}

	ns.logger.Debug("broadcasting message", "peers", len(peers))
	return nil
}

// sendMessage creates a stream to the given peer and sends the message.
func (ns *NetworkServer) sendMessage(peerID p2ppeer.ID, message []byte) {
	// Check if context is already canceled
	if ns.ctx.Err() != nil {
		ns.logger.Debug("context canceled, not sending message", "peer", peerID.String())
		return
	}

	// Create stream with timeout
	ctx, cancel := context.WithTimeout(ns.ctx, defaultDialTimeout)
	defer cancel()

	s, err := ns.host.NewStream(ctx, peerID, ns.protocol)
	if err != nil {
		ns.logger.Error("failed to create stream to peer", "peer", peerID.String(), "error", err)
		
		// Remove peer if we can't connect to it
		ns.mu.Lock()
		delete(ns.peers, peerID)
		ns.mu.Unlock()
		
		return
	}
	defer s.Close()

	_, err = s.Write(message)
	if err != nil {
		ns.logger.Error("failed to send message", "peer", peerID.String(), "error", err)
		return
	}

	// Wait for acknowledgment or handle response if needed
	buf := make([]byte, 1)
	_, err = io.ReadFull(s, buf)
	if err != nil && err != io.EOF {
		ns.logger.Error("error reading ack", "peer", peerID.String(), "error", err)
		return
	}

	ns.logger.Debug("message sent successfully", "peer", peerID.String())
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
	ns.logger.Info("shutting down network server")
	
	// Cancel context first to stop ongoing operations
	ns.cancel()
	
	// Close all connections
	ns.mu.Lock()
	ns.peers = make(map[p2ppeer.ID]struct{})
	ns.mu.Unlock()
	
	// Close the host
	if err := ns.host.Close(); err != nil {
		ns.logger.Error("error during host close", "error", err)
		return fmt.Errorf("error closing libp2p host: %w", err)
	}
	
	ns.logger.Info("network server shut down successfully")
	return nil
}

// defaultDialTimeout is the timeout for creating new streams
const defaultDialTimeout = 10 * time.Second
