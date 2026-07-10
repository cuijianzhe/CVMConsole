package security

import "time"

const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

const (
	TokenTypeAccess              = "access"
	TokenTypeBootstrap           = "bootstrap"
	TokenTypeLogin               = "login"
	TokenTypeHighRisk            = "high_risk"
	TokenTypePasswordResetSelect = "password_reset_select"
	TokenTypePasswordReset       = "password_reset"
)

const (
	ChallengeMethodEmail    = "email"
	ChallengeMethodTOTP     = "totp"
	ChallengeMethodRecovery = "recovery"
)

const (
	ChallengePurposeBindEmail      = "bind_email"
	ChallengePurposeLoginEmail     = "login_email_verify"
	ChallengePurposeHighRiskEmail  = "high_risk_email_verify"
	ChallengePurposeSMTPTest       = "smtp_test"
	ChallengePurposePasswordForgot = "password_forgot_verify"
)

const (
	ActionTokenPurposePasswordReset = "password_reset"
)

const (
	LoginVerificationWindow  = 24 * time.Hour
	HighRiskEmailTrustWindow = time.Hour
	PasswordResetLinkTTL     = time.Hour
	EmailCodeTTL             = 10 * time.Minute
)
