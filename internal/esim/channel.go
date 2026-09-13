package esim

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/1239t/vohive/internal/modem"
)

// ModemChannel 实现 euicc-go 的 driver.SmartCardChannel 接口
// 将 eUICC APDU 请求桥接到 modem.Manager 的 AT 命令执行框架
type ModemChannel struct {
	modem       *modem.Manager
	channel     byte // 当前打开的 logical channel 号
	mu          sync.Mutex
	useCSIM     bool // explicit per-device choice; never retry writes across transports
	traceDelete bool // scoped to DeleteProfile and its GET RESPONSE continuation
}

// NewModemChannel 创建一个新的 ModemChannel
func NewModemChannel(m *modem.Manager) *ModemChannel {
	return &ModemChannel{modem: m}
}

func (c *ModemChannel) CurrentChannel() byte {
	return c.channel
}

// Connect 连接到 APDU 接口（modem 已由外部管理，此处为空操作）
func (c *ModemChannel) Connect() error {
	return nil
}

// Disconnect 断开 APDU 接口连接（modem 由外部管理，此处为空操作）
func (c *ModemChannel) Disconnect() error {
	return nil
}

// OpenLogicalChannel 通过 AT+CCHO 打开 logical channel 并选择指定 AID
// 返回 channel 号
// 注意：不在此处做 ClearLogicalChannels，由上层 Manager 在遍历开始前统一预清理，
// 避免对 SIM 卡频繁发送通道指令导致卡片进入保护状态。
func (c *ModemChannel) OpenLogicalChannel(AID []byte) (byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	aidHex := fmt.Sprintf("%X", AID)
	ch, err := c.modem.OpenLogicalChannel(aidHex)
	if err != nil {
		return 0, fmt.Errorf("打开 logical channel 失败 (AID=%s): %w", aidHex, err)
	}
	c.traceDelete = false
	c.channel = byte(ch)
	return c.channel, nil
}

// Transmit 按设备配置通过 AT+CGLA 或 AT+CSIM 在 logical channel 上传输 APDU。
// 输入原始二进制 APDU 命令，返回原始二进制 APDU 响应
func (c *ModemChannel) Transmit(command []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(command) >= 7 && command[1] == 0xE2 && command[3] == 0 {
		c.traceDelete = command[5] == 0xBF && command[6] == 0x33
	}

	if c.useCSIM {
		var err error
		command, err = logicalCSIMCommand(command, c.channel)
		if err != nil {
			return nil, err
		}
	}

	// 将二进制 APDU 编码为 hex 字符串
	cmdHex := fmt.Sprintf("%X", command)

	// 按配置只发送一次；不能在不确定的写入结果后换通道重发。
	started := time.Now()
	var respHex string
	var err error
	if c.useCSIM {
		respHex, err = c.modem.TransmitLogicalAPDUViaCSIM(int(c.channel), cmdHex)
	} else {
		respHex, err = c.modem.TransmitAPDU(int(c.channel), cmdHex)
	}
	if err != nil {
		traceAPDUResult(c.apduTransportName(), c.channel, command, nil, time.Since(started).Milliseconds(), err)
		return nil, fmt.Errorf("APDU 透传失败: %w", err)
	}

	// 将 hex 响应解码为二进制
	respBytes, err := hex.DecodeString(respHex)
	if err != nil {
		traceAPDUResult(c.apduTransportName(), c.channel, command, nil, time.Since(started).Milliseconds(), err)
		return nil, fmt.Errorf("解析 APDU 响应 hex 失败: %w", err)
	}
	if c.traceDelete {
		traceAPDUResult(c.apduTransportName(), c.channel, command, respBytes, time.Since(started).Milliseconds(), nil)
	}

	return respBytes, nil
}

// CloseLogicalChannel 通过 AT+CCHC 关闭 logical channel
func (c *ModemChannel) CloseLogicalChannel(channel byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.modem.CloseLogicalChannel(int(channel)); err != nil {
		return fmt.Errorf("关闭 logical channel %d 失败: %w", channel, err)
	}
	c.traceDelete = false
	c.channel = 0
	return nil
}

func (c *ModemChannel) apduTransportName() string {
	if c.useCSIM {
		return "csim"
	}
	return "cgla"
}

// CSIM carries the channel in CLA, unlike CGLA's separate session argument.
// Raw product-info queries bypass the LPA encoder, so normalize at the adapter.
func logicalCSIMCommand(command []byte, channel byte) ([]byte, error) {
	if channel == 0 || channel > 19 || len(command) < 4 {
		return nil, fmt.Errorf("invalid CSIM logical channel or APDU header")
	}
	result := append([]byte(nil), command...)
	if channel < 4 {
		result[0] = (result[0] & 0x9C) | channel
	} else {
		result[0] = (result[0] & 0xB0) | 0x40 | (channel - 4)
	}
	return result, nil
}
