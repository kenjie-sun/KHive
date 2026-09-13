package swu

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/1239t/swu-go/pkg/crypto"
	"github.com/1239t/swu-go/pkg/ikev2"
	"github.com/1239t/swu-go/pkg/logger"
)

// RekeyIKESA 执行 IKE SA 密钥轮换 (CREATE_CHILD_SA 交换, ProtocolID=IKE)
// RFC 7296 §2.8: 通过新 DH 交换刷新 IKE SA 的 SPIs、密钥和 MsgID
func (s *Session) RekeyIKESA() error {
	s.rekeyMu.Lock()
	defer s.rekeyMu.Unlock()

	if s.Keys == nil || len(s.Keys.SK_d) == 0 {
		return errors.New("IKE SA 未建立，无法 Rekey")
	}

	// 冷却期检查
	if !s.lastRekeyTime.IsZero() && time.Since(s.lastRekeyTime) < 30*time.Second {
		s.Logger.Debug("IKE Rekey 冷却期内，跳过",
			logger.Duration("sinceLast", time.Since(s.lastRekeyTime)))
		return nil
	}

	s.Logger.Info("开始 IKE SA Rekey")

	// 1. 生成新 Nonce
	newNonce, err := crypto.RandomBytes(32)
	if err != nil {
		return fmt.Errorf("生成 Nonce 失败: %v", err)
	}

	// 2. 生成新 DH 密钥对 (MODP-2048)
	newDH, err := crypto.NewDiffieHellman(14) // Group 14 = MODP-2048
	if err != nil {
		return fmt.Errorf("创建 DH 失败: %v", err)
	}
	if err := newDH.GenerateKey(); err != nil {
		return fmt.Errorf("生成 DH 密钥失败: %v", err)
	}

	// 3. 生成新 SPIi (8 字节)
	newSPIiBytes := make([]byte, 8)
	if _, err := rand.Read(newSPIiBytes); err != nil {
		return fmt.Errorf("生成新 SPIi 失败: %v", err)
	}
	newSPIi := binary.BigEndian.Uint64(newSPIiBytes)

	// 4. 构建 SA Proposal（ProtoIKE，SPI = 新 SPIi）
	// 使用当前会话的加密/完整性/PRF/DH 算法
	prop, err := rekeyProposal(s.ikeProposal, ikev2.ProtoIKE, newSPIiBytes)
	if err != nil {
		return err
	}

	saPayload := &ikev2.EncryptedPayloadSA{
		Proposals: []*ikev2.Proposal{prop},
	}

	// 5. KE 载荷（新公钥）
	kePayload := &ikev2.EncryptedPayloadKE{
		DHGroup: ikev2.MODP_2048_bit,
		KEData:  newDH.PublicKeyBytes(),
	}

	// 6. Nonce 载荷
	noncePayload := &ikev2.EncryptedPayloadNonce{NonceData: newNonce}

	// 7. 保存旧密钥（用旧密钥发送请求和解密响应）
	oldSKd := make([]byte, len(s.Keys.SK_d))
	copy(oldSKd, s.Keys.SK_d)
	oldSPIi := s.SPIi
	oldSPIr := s.SPIr

	// 8. 发送 CREATE_CHILD_SA (ProtoIKE)
	payloads := []ikev2.Payload{saPayload, noncePayload, kePayload}
	respData, err := s.sendEncryptedWithRetry(payloads, ikev2.CREATE_CHILD_SA)
	if err != nil {
		return fmt.Errorf("IKE SA Rekey CREATE_CHILD_SA 发送失败: %v", err)
	}

	s.Logger.Info("IKE SA Rekey 收到响应", logger.Int("len", len(respData)))

	// 9. 处理响应
	return s.handleRekeyIKESAResp(respData, newNonce, newDH, newSPIi, oldSKd, oldSPIi, oldSPIr)
}

// handleRekeyIKESAResp 处理 IKE SA Rekey 的 CREATE_CHILD_SA 响应
func (s *Session) handleRekeyIKESAResp(
	data []byte,
	niNonce []byte,
	newDH *crypto.DiffieHellman,
	newSPIi uint64,
	oldSKd []byte,
	oldSPIi, oldSPIr uint64,
) error {
	// 用旧密钥解密
	_, payloads, err := s.decryptAndParse(data)
	if err != nil {
		return fmt.Errorf("IKE SA Rekey 响应解密失败: %v", err)
	}

	// 提取 SA、KE、Nonce 载荷
	var newSPIr uint64
	var respNonce []byte
	var respKE []byte
	var respSA *ikev2.EncryptedPayloadSA
	var respDH ikev2.AlgorithmType

	for _, p := range payloads {
		switch pl := p.(type) {
		case *ikev2.EncryptedPayloadSA:
			respSA = pl
			if len(pl.Proposals) > 0 && len(pl.Proposals[0].SPI) >= 8 {
				newSPIr = binary.BigEndian.Uint64(pl.Proposals[0].SPI[:8])
			}
			// 检查是否有错误通知
		case *ikev2.EncryptedPayloadNonce:
			respNonce = pl.NonceData
		case *ikev2.EncryptedPayloadKE:
			respKE = pl.KEData
			respDH = pl.DHGroup
		case *ikev2.EncryptedPayloadNotify:
			if pl.NotifyType < 16384 {
				return fmt.Errorf("IKE SA Rekey 被拒绝，通知类型: %d", pl.NotifyType)
			}
		}
	}

	if err := validateRekeyProposal(respSA, copySAProposal(s.ikeProposal, 1, nil), 8); err != nil {
		return err
	}
	if respDH != ikev2.MODP_2048_bit {
		return errors.New("IKE rekey response changed DH group")
	}
	if newSPIr == 0 {
		return errors.New("响应中未找到新 SPIr")
	}
	if len(respNonce) == 0 {
		return errors.New("响应中未找到 Nonce")
	}
	if len(respKE) == 0 {
		return errors.New("响应中未找到 KE")
	}

	// 计算新 DH 共享密钥
	if _, err := newDH.ComputeSharedSecret(respKE); err != nil {
		return fmt.Errorf("新 DH 计算失败: %v", err)
	}

	// 派生新 IKE SA 密钥
	// SKEYSEED = prf(SK_d_old, g^ir_new | Ni | Nr)
	newKeys, err := s.GenerateIKESARekeyKeys(
		oldSKd, newDH.SharedKey,
		niNonce, respNonce,
		newSPIi, newSPIr,
	)
	if err != nil {
		return fmt.Errorf("IKE SA Rekey 密钥派生失败: %v", err)
	}

	// 用旧密钥发送 INFORMATIONAL DELETE 通知 ePDG 旧 IKE SA 废弃
	// 参考 strongSwan ike_rekey.c:727 — DELETE 必须在旧 SA 上发送，且需等待响应
	s.Logger.Debug("发送旧 IKE SA DELETE 通知（使用旧密钥）",
		logger.Uint64("oldSPIi", oldSPIi),
		logger.Uint64("oldSPIr", oldSPIr))
	del := &ikev2.EncryptedPayloadDelete{
		ProtocolID: ikev2.ProtoIKE,
		SPISize:    0,
		NumSPIs:    0,
		SPIs:       nil,
	}
	if _, err := s.sendEncryptedWithRetry([]ikev2.Payload{del}, ikev2.INFORMATIONAL); err != nil {
		s.Logger.Warn("发送旧 IKE SA DELETE 失败（继续切换）", logger.Err(err))
	} else {
		s.Logger.Info("旧 IKE SA DELETE 确认完成")
	}

	// 原子切换 IKE SA 状态（DELETE 发送后再切换）
	s.ikeStateMu.Lock()
	s.localResponder = false
	s.SPIi = newSPIi
	s.SPIr = newSPIr
	s.Keys = newKeys
	s.SequenceNumber.Store(0) // 新 IKE SA 的 MsgID 从 0 开始
	s.DH = newDH              // 更新 DH 状态
	s.ikeStateMu.Unlock()

	s.Logger.Info("IKE SA Rekey 成功",
		logger.Uint64("oldSPIi", oldSPIi),
		logger.Uint64("oldSPIr", oldSPIr),
		logger.Uint64("newSPIi", newSPIi),
		logger.Uint64("newSPIr", newSPIr))

	// 更新冷却期时间戳
	s.lastRekeyTime = time.Now()

	// 通知 Timer 重置
	select {
	case s.rekeyResetCh <- struct{}{}:
	default:
	}

	return nil
}
