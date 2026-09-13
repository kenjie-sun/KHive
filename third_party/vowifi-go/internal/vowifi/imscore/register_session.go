package imscore

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/1239t/swu-go/pkg/logger"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"

	"github.com/1239t/vowifi-go/runtimehost/voiceclient"
)

func resolveStableSIPInstance(cfg Config) string {
	if urn := strings.TrimSpace(cfg.SIPInstanceURN); urn != "" {
		return urn
	}
	return voiceclient.NewSIPInstanceURN()
}

type registerPhase string

const (
	registerPhaseInitial registerPhase = "initial"
	registerPhaseAuth    registerPhase = "auth"
	registerPhaseSecure  registerPhase = "secure"
)

type registerSession struct {
	cfg           Config
	swu           voiceclient.SWUTCPDialer
	network       IMSNetwork
	transportMode string
	state         *registerState
	phase         registerPhase
	jitter        bool

	conn        *connRegisterTransport
	callID      string
	cseq        uint32
	localPort   int
	lastRequest *sip.Request
}

func newRegisterSession(cfg Config, swu voiceclient.SWUTCPDialer, network IMSNetwork, transportMode string, attemptIndex int) *registerSession {
	state := &registerState{
		spiC:        randomNonZeroUint32(),
		spiS:        randomNonZeroUint32(),
		portC:       5064,
		portS:       5063,
		sipInstance: resolveStableSIPInstance(cfg),
	}
	localPort := registerAttemptLocalPort(cfg, attemptIndex)
	return &registerSession{
		cfg:           cfg,
		swu:           swu,
		network:       network,
		transportMode: strings.TrimSpace(transportMode),
		state:         state,
		phase:         registerPhaseInitial,
		jitter:        true,
		callID:        uuid.NewString(),
		cseq:          nextRegisterTransportAttemptCSeq(0),
		localPort:     localPort,
	}
}

func (s *registerSession) imsNetwork() IMSNetwork {
	if s == nil {
		return nil
	}
	return s.network
}

func (s *registerSession) dialRegisterConn(ctx context.Context) (*connRegisterTransport, error) {
	if s == nil {
		return nil, fmt.Errorf("imscore: register session is nil")
	}
	if s.conn != nil {
		return s.conn, nil
	}

	if s.localPort <= 0 {
		s.localPort = registerSIPLocalPort(s.cfg)
	}
	transportAddr := effectiveTransportAddr(s.cfg)
	host, portStr, err := net.SplitHostPort(transportAddr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	rip := net.ParseIP(host)
	if rip == nil {
		return nil, fmt.Errorf("invalid transport P-CSCF %q", transportAddr)
	}
	raddr := &net.TCPAddr{IP: rip, Port: port}

	var rawConn net.Conn
	dialCtx := withLocalPort(ctx, s.localPort)
	switch {
	case s.network != nil:
		rawConn, err = s.network.DialContext(dialCtx, "tcp", raddr, s.transportMode, DialOptions{})
	case s.swu != nil:
		rawConn, err = s.swu.DialContextTCP(dialCtx, s.cfg.LocalIP, s.localPort, rip, port)
	default:
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: s.cfg.LocalIP, Port: s.localPort}}
		rawConn, err = d.DialContext(ctx, "tcp", transportAddr)
	}
	if err != nil {
		return nil, fmt.Errorf("register dial %s: %w", transportAddr, err)
	}

	installSIPTrace(s.cfg.TraceID, s.cfg.DeviceID)
	s.conn = newConnRegisterTransport(rawConn, s.cfg.TraceID, s.cfg.DeviceID)
	logger.Info("IMS REGISTER transport connected",
		logger.String("trace_id", strings.TrimSpace(s.cfg.TraceID)),
		logger.String("device_id", strings.TrimSpace(s.cfg.DeviceID)),
		logger.String("transport_mode", s.transportMode),
		logger.String("local", connLocalAddrString(s.conn.conn)),
		logger.String("remote", connRemoteAddrString(s.conn.conn)),
		logger.Int("local_port_hint", s.localPort))
	return s.conn, nil
}

func (s *registerSession) closeConn() {
	if s == nil || s.conn == nil {
		return
	}
	_ = s.conn.Close()
	s.conn = nil
}

func (s *registerSession) logFSM(event, reason string, variantIndex, variantTotal, mechanismCount int, initialAuth string) {
	logger.Info(fmt.Sprintf("FSM(reg): %s", event),
		logger.String("trace_id", strings.TrimSpace(s.cfg.TraceID)),
		logger.String("device_id", strings.TrimSpace(s.cfg.DeviceID)),
		logger.String("phase", string(s.phase)),
		logger.String("registrar", strings.TrimSpace(s.cfg.PCSCFAddr)),
		logger.String("reason", reason),
		logger.Int("variant_index", variantIndex),
		logger.Int("variant_total", variantTotal),
		logger.String("initial_auth", initialAuth),
		logger.Int("security_client_mechanisms", mechanismCount),
	)
}

func (s *registerSession) runInitialRegisterFlow(ctx context.Context) (result *registerResult, runErr error) {
	if s.jitter {
		if err := waitInitialRegisterJitter(ctx, s.cfg); err != nil {
			return nil, err
		}
		s.jitter = false
	}

	transport, err := s.dialRegisterConn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if runErr != nil {
			if installer, ok := s.swu.(voiceclient.PacketTransformInstaller); ok {
				installer.SetPacketTransform(nil)
			}
		}
		s.closeConn()
		if s.state.portSListener != nil {
			_ = s.state.portSListener.Close()
		}
	}()

	variants := initialRegisterVariants(s.cfg)
	var lastErr error
	for i, variant := range variants {
		s.logFSM("initial_register_attempt", "", i+1, len(variants), securityClientMechanismCount(s.cfg.Template), variant.initialAuth)

		res, err := s.registerOnce(ctx, transport, true, variant)
		if err != nil {
			lastErr = err
			continue
		}

		logger.Info("IMS REGISTER initial response",
			logger.String("trace_id", strings.TrimSpace(s.cfg.TraceID)),
			logger.String("pcscf", s.cfg.PCSCFAddr),
			logger.String("initial_auth", variant.initialAuth),
			logger.Bool("include_pani", variant.includePANI),
			logger.Bool("include_cellular", variant.includeCellular),
			logger.Int("status", res.StatusCode),
			logger.String("reason", res.Reason))

		switch res.StatusCode {
		case sip.StatusOK:
			decision, err := decideInitialRegisterSuccessSecurity(s.cfg, res)
			if err != nil {
				return nil, err
			}
			s.logFSM("initial_register_success", decision.reason, i+1, len(variants), securityClientMechanismCount(s.cfg.Template), variant.initialAuth)
			if decision.requireIPSec {
				if err := installIPSecFromChallenge(s.cfg, s.state, res); err != nil {
					return nil, err
				}
				s.phase = registerPhaseSecure
				return nil, fmt.Errorf("initial 200 cannot establish IPsec without an AKA challenge")
			}
			return s.finishRegister(transport, res, s.lastRequest)
		case sip.StatusUnauthorized, sip.StatusProxyAuthRequired:
			s.phase = registerPhaseAuth
			return s.runAuthRegisterPhase(ctx, transport, res)
		default:
			lastErr = &registrarAttemptError{
				pcscf:      s.cfg.PCSCFAddr,
				statusCode: res.StatusCode,
				reason:     res.Reason,
			}
			outcome := decideRegisterFailureOutcome(s.cfg, res.StatusCode, res.Reason, i, len(variants), false)
			if outcome.retryVariant {
				logger.Info("IMS REGISTER initial reject fallback",
					logger.String("trace_id", strings.TrimSpace(s.cfg.TraceID)),
					logger.Int("status", res.StatusCode),
					logger.String("reason", res.Reason),
					logger.Int("variant_index", i+1),
					logger.Int("variant_total", len(variants)),
					logger.String("next_initial_auth", variants[i+1].initialAuth),
					logger.Bool("next_include_pani", variants[i+1].includePANI),
					logger.Bool("next_include_cellular", variants[i+1].includeCellular))
				continue
			}
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("imscore: initial REGISTER variants exhausted")
}

func (s *registerSession) finishRegister(transport *connRegisterTransport, res *sip.Response, req *sip.Request) (*registerResult, error) {
	s.state.secureConn = transport.ReleaseConn()
	result, err := finalizeRegisterSuccess(s.cfg, *s.state, res)
	if err != nil {
		_ = s.state.secureConn.Close()
		return nil, err
	}
	result.portSListener = s.state.portSListener
	s.state.portSListener = nil
	result.request = req
	result.sipInstance = s.state.sipInstance
	for _, h := range res.GetHeaders("Service-Route") {
		result.serviceRoutes = append(result.serviceRoutes, h.Value())
	}
	return result, nil
}

func (s *registerSession) runAuthRegisterPhase(ctx context.Context, transport *connRegisterTransport, challengeRes *sip.Response) (*registerResult, error) {
	lastRes := challengeRes
	for round := 0; round < maxChallengeRounds; round++ {
		req, err := buildRegisterRequest(s.cfg, *s.state, false, initialRegisterVariant{})
		if err != nil {
			return nil, err
		}
		chal, err := selectDigestChallenge(s.cfg, lastRes)
		if err != nil {
			return nil, err
		}
		aka, auth, err := computeAKAAuth(s.cfg, chal, req)
		if err != nil {
			return nil, err
		}
		s.state.ck, s.state.ik = aka.CK, aka.IK
		decision, err := decideSecAgreeAfterChallenge(s.cfg, lastRes)
		if err != nil {
			return nil, err
		}
		// AUTS is answered on the current connection, awaiting a fresh vector.
		if decision.installIPSec && len(aka.CK) > 0 && len(aka.IK) > 0 {
			if s.state.transport != nil {
				return nil, fmt.Errorf("IPsec re-challenge requires a fresh registration attempt")
			}
			if s.swu == nil {
				return nil, fmt.Errorf("IPsec requires a SWu dialer")
			}

			if err := installIPSecFromChallenge(s.cfg, s.state, lastRes); err != nil {
				return nil, err
			}
			if s.state.portSListener == nil {
				listener, err := s.swu.ListenContextTCP(context.WithoutCancel(ctx), s.cfg.LocalIP, s.state.portS)
				if err != nil {
					return nil, err
				}
				s.state.portSListener = listener
			}
			conn, err := dialSecureRegisterConn(ctx, s.cfg, s.swu, *s.state)
			if err != nil {
				return nil, err
			}
			s.closeConn()
			transport = newConnRegisterTransport(conn, s.cfg.TraceID, s.cfg.DeviceID)
			s.conn = transport
			s.localPort = s.state.portC
			req.RemoveHeader("Contact")
			req.AppendHeader(sip.NewHeader("Contact", buildIMSCoreContact(s.cfg, *s.state, s.state.portS)))
			req.AppendHeader(sip.NewHeader("Security-Verify", s.state.verifyHeader))
		}
		authName := "Authorization"
		if lastRes.StatusCode == 407 {
			authName = "Proxy-Authorization"
		}
		req.RemoveHeader("Authorization")
		req.AppendHeader(sip.NewHeader(authName, auth))
		if err := s.decorateRegisterRequest(req); err != nil {
			return nil, err
		}
		lastRes, err = s.sendRegisterRequest(ctx, transport, req)
		if err != nil {
			return nil, err
		}
		if lastRes.StatusCode == 200 {
			return s.finishRegister(transport, lastRes, req)
		}
		if lastRes.StatusCode != 401 && lastRes.StatusCode != 407 {
			return nil, fmt.Errorf("authenticated REGISTER failed: %d %s", lastRes.StatusCode, lastRes.Reason)
		}
	}
	return nil, fmt.Errorf("IMS authentication challenge limit reached")
}

func (s *registerSession) registerOnce(ctx context.Context, transport *connRegisterTransport, initial bool, variant initialRegisterVariant) (*sip.Response, error) {
	req, err := buildRegisterRequest(s.cfg, *s.state, initial, variant)
	if err != nil {
		return nil, err
	}
	if err := s.decorateRegisterRequest(req); err != nil {
		return nil, err
	}
	s.lastRequest = req
	if err := transport.Send(ctx, req); err != nil {
		return nil, err
	}
	return transport.ReadResponse(ctx)
}

func (s *registerSession) answerRegisterChallenge(ctx context.Context, transport *connRegisterTransport, prevReq *sip.Request, prevRes *sip.Response) (*sip.Response, *sip.Request, []byte, []byte, error) {
	if prevReq == nil {
		req, err := buildRegisterRequest(s.cfg, *s.state, false, initialRegisterVariant{})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		prevReq = req
	}

	chal, err := selectDigestChallenge(s.cfg, prevRes)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	akaResult, authHeader, err := computeAKAAuth(s.cfg, chal, prevReq)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	newReq := prevReq.Clone()
	newReq.RemoveHeader("Via")
	newReq.RemoveHeader("Authorization")
	newReq.AppendHeader(sip.NewHeader("Authorization", authHeader))
	if err := s.decorateRegisterRequest(newReq); err != nil {
		return nil, nil, nil, nil, err
	}

	res, err := s.sendRegisterRequest(ctx, transport, newReq)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return res, newReq, akaResult.CK, akaResult.IK, nil
}

func (s *registerSession) sendRegisterRequest(ctx context.Context, transport *connRegisterTransport, req *sip.Request) (*sip.Response, error) {
	if err := transport.Send(ctx, req); err != nil {
		return nil, err
	}
	return transport.ReadResponse(ctx)
}

func (s *registerSession) decorateRegisterRequest(req *sip.Request) error {
	if req == nil {
		return fmt.Errorf("missing REGISTER request")
	}
	req.RemoveHeader("Via")
	req.RemoveHeader("Call-ID")
	req.RemoveHeader("CSeq")
	req.RemoveHeader("Max-Forwards")

	if s.localPort <= 0 {
		s.localPort = registerSIPLocalPort(s.cfg)
	}
	viaHost := formatRegisterViaHost(s.cfg.LocalIP, s.localPort)
	via := fmt.Sprintf("SIP/2.0/TCP %s;branch=%s", viaHost, sip.GenerateBranchN(16))
	req.PrependHeader(sip.NewHeader("Via", via))
	req.AppendHeader(sip.NewHeader("Call-ID", s.callID))
	req.AppendHeader(sip.NewHeader("CSeq", fmt.Sprintf("%d REGISTER", s.cseq)))
	req.AppendHeader(sip.NewHeader("Max-Forwards", "70"))
	s.cseq = nextRegisterTransportAttemptCSeq(s.cseq)
	return nil
}

func formatRegisterViaHost(ip net.IP, port int) string {
	if ip == nil {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	if ip.To4() == nil {
		return fmt.Sprintf("[%s]:%d", ip.String(), port)
	}
	return fmt.Sprintf("%s:%d", ip.String(), port)
}
