package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	broker "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

func jsonDecode(data []byte, out any) error { return json.Unmarshal(data, out) }

type brokerAuth struct {
	broker.HookBase
	user, password, internalID, internalPassword, prefix string
}

func (h *brokerAuth) ID() string { return "phicomm-auth" }
func (h *brokerAuth) Provides(b byte) bool {
	return b == broker.OnConnectAuthenticate || b == broker.OnACLCheck || b == broker.OnPublish
}
func sameSecret(a []byte, b string) bool { return subtle.ConstantTimeCompare(a, []byte(b)) == 1 }
func (h *brokerAuth) OnConnectAuthenticate(cl *broker.Client, pk packets.Packet) bool {
	if cl.ID == h.internalID {
		return sameSecret(pk.Connect.Username, "__manager_internal") && sameSecret(pk.Connect.Password, h.internalPassword)
	}
	return sameSecret(pk.Connect.Username, h.user) && sameSecret(pk.Connect.Password, h.password)
}
func (h *brokerAuth) scoped(topic string) bool {
	if strings.HasPrefix(topic, h.prefix+"/") || strings.HasPrefix(topic, "phicomm/linkage/"+userKey(h.user)+"/") {
		return true
	}
	parts := strings.Split(topic, "/")
	return len(parts) >= 3 && parts[0] == "homeassistant" && parts[2] == userKey(h.user)
}
func (h *brokerAuth) OnACLCheck(cl *broker.Client, topic string, write bool) bool {
	if !write && topic == "homeassistant/status" {
		return true
	}
	if !h.scoped(topic) {
		return false
	}
	if write && strings.HasSuffix(topic, "/command") && cl.ID != h.internalID {
		return false
	}
	return true
}
func (h *brokerAuth) OnPublish(cl *broker.Client, pk packets.Packet) (packets.Packet, error) {
	if len(pk.Payload) > 262144 || !h.OnACLCheck(cl, pk.TopicName, true) || (strings.HasSuffix(pk.TopicName, "/command") && pk.FixedHeader.Retain) {
		return pk, packets.ErrRejectPacket
	}
	return pk, nil
}

type mqttPacket struct {
	tail     string
	payload  []byte
	retained bool
}
type pendingCommand struct {
	topic  string
	result chan Obj
}
type MQTT struct {
	mu             sync.Mutex
	client         paho.Client
	broker         *broker.Server
	prefix         string
	state, message string
	pending        map[string]pendingCommand
	timeout        time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	onPacket       func(string, []byte, bool)
	onOnline       func(bool)
}

func newMQTT(timeout time.Duration, packet func(string, []byte, bool), online func(bool)) *MQTT {
	return &MQTT{state: "disabled", message: "未启用", pending: map[string]pendingCommand{}, timeout: timeout, onPacket: packet, onOnline: online}
}
func (m *MQTT) status() Obj {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Obj{"state": m.state, "message": m.message}
}
func (m *MQTT) setStatus(state, message string) {
	m.mu.Lock()
	m.state = state
	m.message = message
	m.mu.Unlock()
}
func (m *MQTT) start(config Obj, s *Store) error {
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.done = make(chan struct{})
	events := make(chan mqttPacket, 128)
	go func() {
		defer close(m.done)
		for {
			select {
			case <-m.ctx.Done():
				return
			case event := <-events:
				if m.onPacket != nil {
					m.onPacket(event.tail, event.payload, event.retained)
				}
			}
		}
	}()
	m.prefix = "phicomm/v2/" + userKey(str(config["username"]))
	if config["mode"] == "disabled" {
		m.setStatus("disabled", "未启用")
		return nil
	}
	password, err := s.decrypt(str(config["password"]))
	if err != nil {
		m.stop()
		return err
	}
	m.setStatus("connecting", "连接中")
	internalID := "manager_" + token()
	user := str(config["username"])
	port := int(num(config["port"]))
	host := str(config["host"])
	scheme := "tcp"
	if boolean(config["tls"]) {
		scheme = "ssl"
	}
	if config["mode"] == "embedded" {
		internalPassword := token() + token()
		srv := broker.New(&broker.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		srv.Options.Capabilities.MaximumClients = 256
		srv.Options.Capabilities.MaximumPacketSize = 263168
		if err = srv.AddHook(&brokerAuth{user: user, password: password, internalID: internalID, internalPassword: internalPassword, prefix: m.prefix}, nil); err != nil {
			srv.Close()
			m.stop()
			return err
		}
		listener := listeners.NewTCP(listeners.Config{ID: "mqtt", Address: fmt.Sprintf("0.0.0.0:%d", port)})
		if err = srv.AddListener(listener); err != nil {
			srv.Close()
			m.setStatus("error", "MQTT 端口已被占用或无法监听")
			m.stop()
			return failure("MQTT 端口已被占用或无法监听", 422, "mqtt_start_failed")
		}
		m.broker = srv
		if err = srv.Serve(); err != nil {
			m.stop()
			return failure("MQTT 服务启动失败", 422, "mqtt_start_failed")
		}
		host = "127.0.0.1"
		scheme = "tcp"
		user = "__manager_internal"
		password = internalPassword
	}
	opts := paho.NewClientOptions().AddBroker(scheme + "://" + net.JoinHostPort(host, fmt.Sprint(port))).SetClientID(internalID).SetUsername(user).SetPassword(password).SetProtocolVersion(4).SetCleanSession(true).SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(3 * time.Second).SetMaxReconnectInterval(3 * time.Second).SetConnectTimeout(5 * time.Second).SetWriteTimeout(5 * time.Second).SetOrderMatters(true)
	opts.SetOnConnectHandler(func(c paho.Client) {
		if m.ctx.Err() != nil {
			return
		}
		if m.onOnline != nil {
			m.onOnline(true)
		}
		token := c.Subscribe(m.prefix+"/#", 1, nil)
		finished := token.WaitTimeout(5 * time.Second)
		if m.ctx.Err() != nil {
			return
		}
		granted := true
		if sub, ok := token.(*paho.SubscribeToken); ok {
			granted = sub.Result()[m.prefix+"/#"] != 128
		}
		if !finished || token.Error() != nil || !granted {
			m.setStatus("error", "Broker 未授权设备主题")
			return
		}
		message := "已连接外部 Broker"
		if config["mode"] == "embedded" {
			message = "内置服务运行中"
		}
		m.setStatus("connected", message)
	})
	opts.SetConnectionLostHandler(func(c paho.Client, err error) {
		if m.ctx.Err() != nil {
			return
		}
		m.setStatus("disconnected", "MQTT 已断开，正在重连")
		if m.onOnline != nil {
			m.onOnline(false)
		}
	})
	opts.SetReconnectingHandler(func(c paho.Client, _ *paho.ClientOptions) {
		if m.ctx.Err() == nil {
			m.setStatus("connecting", "MQTT 正在重连，请检查地址、账号与权限")
		}
	})
	opts.SetDefaultPublishHandler(func(_ paho.Client, msg paho.Message) {
		if len(msg.Payload()) > 262144 || !strings.HasPrefix(msg.Topic(), m.prefix+"/") {
			return
		}
		tail := strings.TrimPrefix(msg.Topic(), m.prefix+"/")
		if strings.HasSuffix(tail, "/result") {
			if msg.Retained() {
				return
			}
			var response Obj
			if json.Unmarshal(msg.Payload(), &response) != nil {
				return
			}
			m.mu.Lock()
			pending, ok := m.pending[str(response["request_id"])]
			m.mu.Unlock()
			if ok && pending.topic == msg.Topic() {
				select {
				case pending.result <- response:
				default:
				}
			}
			return
		}
		event := mqttPacket{tail, append([]byte(nil), msg.Payload()...), msg.Retained()}
		select {
		case events <- event:
		case <-m.ctx.Done():
		default:
		}
	})
	client := paho.NewClient(opts)
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()
	client.Connect()
	return nil
}
func (m *MQTT) command(parent context.Context, d, request Obj) (Obj, error) {
	m.mu.Lock()
	client := m.client
	connected := m.state == "connected" && client != nil && client.IsConnected()
	m.mu.Unlock()
	if !connected {
		return nil, failure("MQTT 尚未连接", 503, "mqtt_unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()
	base := m.prefix + "/" + strings.ToLower(str(d["model"])) + "/" + str(d["mac"])
	id := str(request["request_id"])
	pending := pendingCommand{base + "/result", make(chan Obj, 1)}
	m.mu.Lock()
	m.pending[id] = pending
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.pending, id); m.mu.Unlock() }()
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	sent := client.Publish(base+"/command", 0, false, raw)
	select {
	case <-sent.Done():
		if sent.Error() != nil {
			return nil, failure("MQTT 发送中断，操作结果未确认", 502, "transport_error")
		}
	case <-m.ctx.Done():
		return nil, failure("MQTT 已断开，操作结果未确认", 502, "transport_error")
	case <-ctx.Done():
		return nil, failure("MQTT 设备未应答，操作结果未确认", 504, "transport_error")
	}
	select {
	case response := <-pending.result:
		return result(response, request)
	case <-m.ctx.Done():
		return nil, failure("MQTT 已断开，操作结果未确认", 502, "transport_error")
	case <-ctx.Done():
		return nil, failure("MQTT 设备未应答，操作结果未确认", 504, "transport_error")
	}
}
func (m *MQTT) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	client := m.client
	m.client = nil
	m.mu.Unlock()
	if client != nil {
		client.Disconnect(0)
	}
	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}
	if m.done != nil {
		<-m.done
		m.done = nil
	}
}
