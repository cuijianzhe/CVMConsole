package service

import (
	"fmt"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPSetupInfo 2FA 绑定信息
type TOTPSetupInfo struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
}

// GenerateTOTPSetup 生成 2FA 配置
func GenerateTOTPSetup(username string) (*TOTPSetupInfo, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "QVMConsole",
		AccountName: strings.TrimSpace(username),
		Algorithm:   otp.AlgorithmSHA1,
		Digits:      otp.DigitsSix,
		Period:      30,
		SecretSize:  20,
	})
	if err != nil {
		return nil, err
	}
	return &TOTPSetupInfo{
		Secret:     key.Secret(),
		OtpauthURL: key.URL(),
	}, nil
}

// ValidateTOTPCode 校验 TOTP 验证码
func ValidateTOTPCode(secret, code string) error {
	valid := totp.Validate(strings.TrimSpace(code), strings.TrimSpace(secret))
	if !valid {
		return fmt.Errorf("2FA 验证码错误")
	}
	return nil
}
