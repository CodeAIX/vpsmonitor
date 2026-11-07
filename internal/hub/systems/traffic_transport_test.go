//go:build testing

package systems

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/fxamacker/cbor/v2"
	"github.com/gliderlabs/ssh"
	"github.com/CodeAIX/vpsmonitor/internal/common"
	esystem "github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/CodeAIX/vpsmonitor/internal/hub/expirymap"
	"github.com/CodeAIX/vpsmonitor/internal/hub/ws"
	"github.com/lxzan/gws"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func TestTrafficWebSocketAndSSHIngestion(t *testing.T) {
	for _, transport := range []string{"ws", "ssh"} {
		for _, withTraffic := range []bool{false, true} {
			t.Run(transport+map[bool]string{false: "_old", true: "_plus"}[withTraffic], func(t *testing.T) {
				sys, app := newTestSystemWithHub(t)
				sys.manager.zfsFetchMap = expirymap.New[zfsFetchState](time.Hour)
				t.Cleanup(sys.manager.zfsFetchMap.StopCleaner)
				sys.ctx = t.Context()
				sys.detailsFetched.Store(true)
				sys.manager.ctx, sys.manager.cancel = context.WithCancel(t.Context())
				sys.manager.traffic = &trafficWriter{manager: sys.manager, pending: make(map[string]*traffic.Snapshot)}
				t.Cleanup(func() { sys.manager.cancel(); sys.manager.traffic.stop(); sys.manager.updaters.Wait() })
				data := esystem.CombinedData{Info: esystem.Info{Uptime: 42}, Stats: esystem.Stats{Cpu: 12}}
				if withTraffic {
					data.Traffic = trafficTestSnapshot()
				}
				if transport == "ws" {
					connections := make(chan *ws.WsConn, 1)
					upgrader := gws.NewUpgrader(&monitorSyncServer{}, nil)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						conn, err := upgrader.Upgrade(w, r)
						if err != nil {
							t.Error(err)
							return
						}
						wrapped := ws.NewWsConnection(conn, semver.MustParse("0.21.1"))
						conn.Session().Store("wsConn", wrapped)
						connections <- wrapped
						conn.ReadLoop()
					}))
					t.Cleanup(server.Close)
					client := &sequenceDataClient{responses: make(chan esystem.CombinedData, 1)}
					client.responses <- data
					conn, _, err := gws.NewClient(client, &gws.ClientOption{Addr: "ws" + strings.TrimPrefix(server.URL, "http")})
					require.NoError(t, err)
					t.Cleanup(func() { _ = conn.NetConn().Close() })
					go conn.ReadLoop()
					select {
					case sys.WsConn = <-connections:
					case <-time.After(3 * time.Second):
						t.Fatal("websocket did not connect")
					}
				} else {
					server := &ssh.Server{Version: "beszel_0.21.1", Handler: func(session ssh.Session) {
						var request common.HubRequest[cbor.RawMessage]
						if err := cbor.NewDecoder(session).Decode(&request); err != nil {
							t.Error(err)
							return
						}
						raw, err := cbor.Marshal(data)
						if err != nil {
							t.Error(err)
							return
						}
						response := common.AgentResponse{Data: raw}
						if !withTraffic {
							response = common.AgentResponse{SystemData: &data}
						}
						if err := cbor.NewEncoder(session).Encode(response); err != nil {
							t.Error(err)
						}
						_ = session.Exit(0)
					}}
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					t.Cleanup(func() { _ = server.Close() })
					go server.Serve(listener)
					sys.Host, sys.Port, err = net.SplitHostPort(listener.Addr().String())
					require.NoError(t, err)
					sys.manager.sshConfig = &gossh.ClientConfig{User: "test", HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: time.Second}
					t.Cleanup(sys.closeSSHConnection)
				}
				require.NoError(t, sys.update())
				sys.manager.updaters.Wait()
				stats, err := app.CountRecords("system_stats")
				require.NoError(t, err)
				require.Equal(t, int64(1), stats)
				days, err := app.CountRecords("traffic_daily")
				require.NoError(t, err)
				if withTraffic {
					require.Equal(t, int64(30), days)
				} else {
					require.Zero(t, days)
					count, err := app.CountRecords("traffic_settings")
					require.NoError(t, err)
					require.Zero(t, count)
				}
			})
		}
	}
}
