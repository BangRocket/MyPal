// Package email implements the MessagingPort interface using IMAP for inbound
// message polling and net/smtp for outbound delivery.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/smtp"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomessagemail "github.com/emersion/go-message/mail"
	"github.com/google/uuid"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/models"
	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/BangRocket/MyPal/apps/backend/internal/infrastructure/config"
)

// Adapter implements ports.MessagingPort for email via IMAP + SMTP.
type Adapter struct {
	cfg       config.EmailConfig
	onMessage func(context.Context, *models.Message)
}

// NewAdapter creates a new email adapter. The adapter is inert until Start is
// called, which begins the IMAP polling loop.
func NewAdapter(cfg config.EmailConfig) (*Adapter, error) {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 60
	}
	if cfg.IMAPPort <= 0 {
		cfg.IMAPPort = 993
	}
	if cfg.SMTPPort <= 0 {
		cfg.SMTPPort = 587
	}
	return &Adapter{cfg: cfg}, nil
}

// --------------------------------------------------------------------------
// IMAP inbound
// --------------------------------------------------------------------------

// Start connects to the IMAP server and begins a polling loop in a goroutine.
// It calls onMessage for each unseen email. The loop stops when ctx is
// cancelled.
func (a *Adapter) Start(ctx context.Context, onMessage func(context.Context, *models.Message)) error {
	a.onMessage = onMessage
	go a.pollLoop(ctx)
	return nil
}

// dialIMAP establishes an IMAP connection with optional TLS.
func (a *Adapter) dialIMAP() (*imapclient.Client, error) {
	addr := fmt.Sprintf("%s:%d", a.cfg.IMAPHost, a.cfg.IMAPPort)
	if a.cfg.IMAPTLS {
		return imapclient.DialTLS(addr, &imapclient.Options{
			TLSConfig: &tls.Config{ServerName: a.cfg.IMAPHost},
		})
	}
	return imapclient.DialInsecure(addr, nil)
}

// pollLoop runs the IMAP fetch/mark cycle on a ticker until ctx is done.
func (a *Adapter) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(a.cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	// Run once immediately, then on tick.
	a.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.poll(ctx)
		}
	}
}

// poll fetches unseen messages, converts them, and marks them as seen.
func (a *Adapter) poll(ctx context.Context) {
	c, err := a.dialIMAP()
	if err != nil {
		log.Printf("email: IMAP dial error: %v", err)
		return
	}
	defer c.Close()

	if err := c.Login(a.cfg.IMAPUser, a.cfg.IMAPPass).Wait(); err != nil {
		log.Printf("email: IMAP login error: %v", err)
		return
	}

	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		log.Printf("email: IMAP select INBOX error: %v", err)
		return
	}

	// Search for unseen messages.
	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}
	searchData, err := c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		log.Printf("email: IMAP search error: %v", err)
		return
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return
	}

	// Build a UID set from the found UIDs.
	uidSet := new(imap.UIDSet)
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	// Fetch envelope + full body for each message.
	bodySection := &imap.FetchItemBodySection{} // BODY[] — full RFC 5322
	fetchOpts := &imap.FetchOptions{
		Envelope:    true,
		Flags:       true,
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}

	fetchCmd := c.Fetch(*uidSet, fetchOpts)
	defer fetchCmd.Close()

	var fetchedUIDs []imap.UID

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		envelope, body, uid := a.collectFetchData(msg)
		if envelope == nil || body == nil {
			continue
		}

		// Always mark as seen so we don't re-fetch filtered messages.
		fetchedUIDs = append(fetchedUIDs, uid)

		// Apply inbound filters before processing.
		fromAddr := ""
		if len(envelope.From) > 0 {
			fromAddr = envelope.From[0].Addr()
		}
		toAddr := ""
		if len(envelope.To) > 0 {
			toAddr = envelope.To[0].Addr()
		}
		if !a.shouldProcess(fromAddr, toAddr, envelope.Subject) {
			log.Printf("email: filtered out message from=%s subject=%q", fromAddr, envelope.Subject)
			continue
		}

		domainMsg := a.convertToDomainMessage(envelope, body)
		if domainMsg != nil {
			a.onMessage(ctx, domainMsg)
		}
	}
	if err := fetchCmd.Close(); err != nil {
		log.Printf("email: IMAP fetch error: %v", err)
	}

	// Mark fetched messages as \Seen.
	if len(fetchedUIDs) > 0 {
		markSet := new(imap.UIDSet)
		for _, uid := range fetchedUIDs {
			markSet.AddNum(uid)
		}
		storeFlags := &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagSeen},
			Silent: true,
		}
		if err := c.Store(*markSet, storeFlags, nil).Close(); err != nil {
			log.Printf("email: IMAP store flags error: %v", err)
		}
	}
}

// collectFetchData iterates over a single fetched message's items and extracts
// the envelope and body literal.
func (a *Adapter) collectFetchData(msg *imapclient.FetchMessageData) (*imap.Envelope, []byte, imap.UID) {
	var envelope *imap.Envelope
	var body []byte
	var uid imap.UID

	for {
		item := msg.Next()
		if item == nil {
			break
		}
		switch data := item.(type) {
		case imapclient.FetchItemDataEnvelope:
			envelope = data.Envelope
		case imapclient.FetchItemDataUID:
			uid = data.UID
		case imapclient.FetchItemDataBodySection:
			b, err := io.ReadAll(data.Literal)
			if err != nil {
				log.Printf("email: error reading body: %v", err)
				continue
			}
			body = b
		}
	}
	return envelope, body, uid
}

// convertToDomainMessage parses the raw RFC 5322 body and IMAP envelope into a
// models.Message.
func (a *Adapter) convertToDomainMessage(env *imap.Envelope, rawBody []byte) *models.Message {
	// Extract sender info from envelope.
	senderEmail := ""
	senderName := ""
	if len(env.From) > 0 {
		from := env.From[0]
		senderEmail = from.Addr()
		senderName = from.Name
		if senderName == "" {
			senderName = senderEmail
		}
	}

	// Parse body to extract plain text.
	plainText := extractPlainText(rawBody)

	// Build Message-ID and In-Reply-To from envelope.
	messageID := env.MessageID
	inReplyTo := env.InReplyTo

	metadata := map[string]interface{}{
		"channel_type": "email",
		"subject":      env.Subject,
		"message_id":   messageID,
		"in_reply_to":  inReplyTo,
	}

	return &models.Message{
		ID:         uuid.New(),
		ChannelID:  senderEmail,
		Content:    plainText,
		SenderName: senderName,
		SenderID:   senderEmail,
		Timestamp:  env.Date,
		Metadata:   metadata,
	}
}

// extractPlainText parses an RFC 5322 message body and returns the first
// text/plain part. Falls back to the raw body as a string if parsing fails.
func extractPlainText(rawBody []byte) string {
	mr, err := gomessagemail.CreateReader(bytes.NewReader(rawBody))
	if err != nil {
		// Fallback: return raw body as-is (may contain headers).
		return string(rawBody)
	}

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		// Prefer text/plain inline parts.
		if h, ok := p.Header.(*gomessagemail.InlineHeader); ok {
			ct, _, _ := h.ContentType()
			if ct == "text/plain" || ct == "" {
				b, readErr := io.ReadAll(p.Body)
				if readErr == nil {
					return string(b)
				}
			}
		}
	}

	return ""
}

// --------------------------------------------------------------------------
// Filtering
// --------------------------------------------------------------------------

// shouldProcess evaluates configured filters to decide whether an email should
// be processed. If no filters are configured, all emails are processed.
//
// Semantics:
//  1. If any "ignore" filter matches, the email is skipped.
//  2. If "process" filters exist and none match, the email is skipped.
//  3. Otherwise the email is processed.
func (a *Adapter) shouldProcess(from, to, subject string) bool {
	filters := a.cfg.Filters
	if len(filters) == 0 {
		return true // no filters → process everything
	}

	hasProcessFilter := false
	processMatched := false

	for _, f := range filters {
		re, err := regexp.Compile(f.Pattern)
		if err != nil {
			log.Printf("email: invalid filter pattern %q: %v", f.Pattern, err)
			continue
		}

		var value string
		switch strings.ToLower(f.Field) {
		case "from":
			value = from
		case "to":
			value = to
		case "subject":
			value = subject
		default:
			log.Printf("email: unknown filter field %q, skipping", f.Field)
			continue
		}

		matched := re.MatchString(value)

		switch strings.ToLower(f.Action) {
		case "ignore":
			if matched {
				return false
			}
		case "process":
			hasProcessFilter = true
			if matched {
				processMatched = true
			}
		default:
			log.Printf("email: unknown filter action %q, skipping", f.Action)
		}
	}

	// If process filters exist, at least one must match.
	if hasProcessFilter && !processMatched {
		return false
	}

	return true
}

// --------------------------------------------------------------------------
// SMTP outbound
// --------------------------------------------------------------------------

// SendMessage sends a plain-text email via SMTP to msg.ChannelID (the
// recipient email address).
func (a *Adapter) SendMessage(ctx context.Context, msg *models.Message) error {
	to := msg.ChannelID
	if to == "" {
		return fmt.Errorf("email: empty recipient (ChannelID)")
	}

	subject := "Message from MyPal"
	if msg.Metadata != nil {
		if s, ok := msg.Metadata["subject"].(string); ok && s != "" {
			subject = s
		}
	}

	inReplyTo := ""
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["in_reply_to"].(string); ok {
			inReplyTo = v
		}
	}

	return a.sendSMTP(to, subject, msg.Content, inReplyTo)
}

// sendSMTP composes and sends a plain-text email.
func (a *Adapter) sendSMTP(to, subject, body, inReplyTo string) error {
	from := a.cfg.SMTPFrom
	addr := fmt.Sprintf("%s:%d", a.cfg.SMTPHost, a.cfg.SMTPPort)

	// Build the raw message.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%s@%s>\r\n", uuid.New().String(), a.cfg.SMTPHost)
	if inReplyTo != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", inReplyTo)
		fmt.Fprintf(&buf, "References: %s\r\n", inReplyTo)
	}
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "\r\n")
	buf.WriteString(body)

	auth := smtp.PlainAuth("", a.cfg.SMTPUser, a.cfg.SMTPPass, a.cfg.SMTPHost)

	if a.cfg.SMTPTLS {
		return a.sendSMTPStartTLS(addr, auth, from, to, buf.Bytes())
	}
	return smtp.SendMail(addr, auth, from, []string{to}, buf.Bytes())
}

// sendSMTPStartTLS connects to the SMTP server and upgrades to TLS via
// STARTTLS before authenticating and sending.
func (a *Adapter) sendSMTPStartTLS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return fmt.Errorf("email: SMTP dial error: %w", err)
	}

	host := a.cfg.SMTPHost
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: SMTP client error: %w", err)
	}
	defer c.Close()

	if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
		return fmt.Errorf("email: STARTTLS error: %w", err)
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("email: SMTP auth error: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: SMTP MAIL FROM error: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("email: SMTP RCPT TO error: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: SMTP DATA error: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("email: SMTP write error: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: SMTP close error: %w", err)
	}
	return c.Quit()
}

// --------------------------------------------------------------------------
// Other MessagingPort methods
// --------------------------------------------------------------------------

// SendMedia sends an email with the media URL or caption as body. Email does
// not natively support rich media inline, so we include a link/caption.
func (a *Adapter) SendMedia(ctx context.Context, media *ports.Media) error {
	to := media.ChatID
	if to == "" {
		return fmt.Errorf("email: empty recipient (ChatID)")
	}
	subject := "Media from MyPal"
	body := media.Caption
	if media.URL != "" {
		if body != "" {
			body += "\n\n"
		}
		body += media.URL
	}
	return a.sendSMTP(to, subject, body, "")
}

// SendTyping is a no-op for email.
func (a *Adapter) SendTyping(_ context.Context, _ string) error {
	return nil
}

// HandleWebhook is a no-op — email uses IMAP polling, not webhooks.
func (a *Adapter) HandleWebhook(_ context.Context, _ []byte) (*models.Message, error) {
	return nil, nil
}

// GetUserInfo returns the email address as both ID and display name.
func (a *Adapter) GetUserInfo(_ context.Context, userID string) (*ports.UserInfo, error) {
	name := userID
	if idx := strings.Index(userID, "@"); idx > 0 {
		name = userID[:idx]
	}
	return &ports.UserInfo{
		ID:          userID,
		Username:    userID,
		DisplayName: name,
	}, nil
}

// React is a no-op for email.
func (a *Adapter) React(_ context.Context, _ string, _ string) error {
	return nil
}

// GetCapabilities returns email-specific capabilities (text only).
func (a *Adapter) GetCapabilities() ports.ChannelCapabilities {
	return ports.ChannelCapabilities{
		HasVoiceMessage: false,
		HasCallStream:   false,
		HasTextStream:   true,
		HasMediaSupport: false,
	}
}

// ConvertAudioForPlatform returns the original data unchanged — email has no
// special audio format requirements.
func (a *Adapter) ConvertAudioForPlatform(_ context.Context, audioData []byte, format string) ([]byte, string, error) {
	return audioData, format, nil
}
