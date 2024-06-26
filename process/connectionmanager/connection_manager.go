package connectionmanager

import (
	"context"
	"sync"
	"time"

	"github.com/aarthikrao/timeMachine/components/dht"
	js "github.com/aarthikrao/timeMachine/components/jobstore"
	"github.com/aarthikrao/timeMachine/components/network"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	ErrNodeNotPresent = errors.New("node not present")
)

// This will aggregate all the connections and clients for
// the GRPC connection with other time machine node.
type timeMachineConnection struct {
	// The uri of the time machine instance
	address string

	// The main grpc connection that is created with another instance of time machine node
	grpcConn *grpc.ClientConn

	// All the clients
	jobStore js.JobStoreWithReplicator
}

type ConnectionManager struct {

	// nodeID vs connection object
	tmcMap map[dht.NodeID]*timeMachineConnection
	mu     sync.RWMutex

	rpcTimeout time.Duration

	log *zap.Logger
}

// CreateConnectionManager returns the connection manager
// It does not initialise the connections. This will have to be done
// by using the AddNewConnection
func CreateConnectionManager(log *zap.Logger, rpcTimeout time.Duration) *ConnectionManager {
	return &ConnectionManager{
		log:        log,
		tmcMap:     make(map[dht.NodeID]*timeMachineConnection),
		rpcTimeout: rpcTimeout,
	}
}

// connects to the provided nodeID.
func (cm *ConnectionManager) connect(nodeID dht.NodeID, addr string) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())

	defer cancelFunc()

	if err != nil {
		return err
	}

	cm.tmcMap[nodeID] = &timeMachineConnection{
		address:  addr,
		grpcConn: conn,
		jobStore: network.CreateJobStoreClient(conn, cm.rpcTimeout),
	}

	return nil
}

// Adds new connection to the connection manager
func (cm *ConnectionManager) Add(serverID string, address string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	nodeID := dht.NodeID(serverID)

	if _, ok := cm.tmcMap[nodeID]; ok {
		// This connection already exists
		return nil
	}

	// TODO: Add retry mechanism
	return cm.connect(nodeID, address)
}

// GetJobStore returns an existing job store client
func (cm *ConnectionManager) GetJobStore(nodeID dht.NodeID) (js.JobStoreWithReplicator, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if tmc, ok := cm.tmcMap[nodeID]; ok {
		return tmc.jobStore, nil
	}

	return nil, ErrNodeNotPresent
}

func (cm *ConnectionManager) CheckHealth(nodeId dht.NodeID) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	tmc := cm.tmcMap[nodeId]
	if tmc == nil {
		return false
	}

	switch tmc.grpcConn.GetState() {
	case connectivity.Ready, connectivity.Idle:
		return true
	}

	return false
}

// Closes all the connections maintained by the connection manager
func (cm *ConnectionManager) Close() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for nodeID, tmc := range cm.tmcMap {
		cm.log.Info("Closing connection with node",
			zap.String("nodeID", string(nodeID)),
			zap.String("addr", tmc.address),
		)

		tmc.grpcConn.Close()
	}
}
