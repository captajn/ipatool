package mtproto

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

type Client struct {
	ApiID   int
	ApiHash string
	Token   string

	tgClient *telegram.Client
	api      *tg.Client
	cancel   context.CancelFunc
	running  bool
	mu       sync.Mutex
}

func NewClient(apiID int, apiHash string, token string) *Client {
	return &Client{
		ApiID:   apiID,
		ApiHash: apiHash,
		Token:   token,
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	clientCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.tgClient = telegram.NewClient(c.ApiID, c.ApiHash, telegram.Options{})

	ready := make(chan error, 1)

	go func() {
		err := c.tgClient.Run(clientCtx, func(ctx context.Context) error {
			if _, err := c.tgClient.Auth().Bot(ctx, c.Token); err != nil {
				return err
			}

			c.api = tg.NewClient(c.tgClient)
			c.running = true
			ready <- nil

			<-ctx.Done()
			return ctx.Err()
		})
		if err != nil {
			if !c.running {
				ready <- err
			}
			log.Printf("MTProto client stopped: %v", err)
		}
		c.running = false
	}()

	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		c.Disconnect()
		return ctx.Err()
	}
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	c.running = false
}

type ProgressWriter struct {
	Total      int64
	Downloaded int64
	OnProgress func(downloaded, total int64)
	Writer     io.Writer
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.Writer.Write(p)
	pw.Downloaded += int64(n)
	if pw.OnProgress != nil {
		pw.OnProgress(pw.Downloaded, pw.Total)
	}
	return n, err
}

func (c *Client) DownloadFile(ctx context.Context, msgID int, outputPath string, progress func(downloaded, total int64)) error {
	if !c.running {
		return fmt.Errorf("MTProto client not connected")
	}

	// Get message to find the media
	res, err := c.api.MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: msgID},
	})
	if err != nil {
		return err
	}

	var msg *tg.Message
	switch m := res.(type) {
	case *tg.MessagesMessages:
		if len(m.Messages) == 0 {
			return fmt.Errorf("message not found")
		}
		msg, _ = m.Messages[0].(*tg.Message)
	case *tg.MessagesMessagesSlice:
		if len(m.Messages) == 0 {
			return fmt.Errorf("message not found")
		}
		msg, _ = m.Messages[0].(*tg.Message)
	default:
		return fmt.Errorf("unexpected response type")
	}

	if msg == nil || msg.Media == nil {
		return fmt.Errorf("no media in message")
	}

	mediaDoc, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return fmt.Errorf("media is not a document")
	}

	doc, ok := mediaDoc.Document.(*tg.Document)
	if !ok {
		return fmt.Errorf("media document is not valid")
	}

	// Prepare output file
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	pw := &ProgressWriter{
		Total:      doc.Size,
		OnProgress: progress,
		Writer:     f,
	}

	// Download
	d := downloader.NewDownloader()
	_, err = d.Download(c.api, doc.AsInputDocumentFileLocation()).
		Stream(ctx, pw)

	return err
}

func (c *Client) UploadFile(ctx context.Context, chatID int64, filePath string, caption string, thumbPath string, replyToMsgID int, progress func(uploaded, total int64)) error {
	if !c.running {
		return fmt.Errorf("MTProto client not connected")
	}

	u := uploader.NewUploader(c.api)

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	pw := &ProgressWriter{
		Total:      info.Size(),
		OnProgress: progress,
		Writer:     io.Discard, // Just for tracking
	}

	// Wrapping reader to track progress
	tr := io.TeeReader(f, pw)

	upload, err := u.FromReader(ctx, info.Name(), tr)
	if err != nil {
		return err
	}

	sender := message.NewSender(c.api).WithUploader(u)
	
	doc := message.UploadedDocument(upload, html.String(nil, caption)).Filename(info.Name())
	
	// Add thumbnail if provided
	if thumbPath != "" {
		thumbFile, err := u.FromPath(ctx, thumbPath)
		if err == nil {
			doc.Thumb(thumbFile)
		}
	}

	// Use Peer Resolver to avoid AccessHash 0 issues
	peer, err := sender.Resolve(fmt.Sprintf("%d", chatID)).AsInputPeer(ctx)
	if err != nil {
		// Fallback to InputPeerUser if resolve fails (though resolve is better)
		peer = &tg.InputPeerUser{UserID: chatID, AccessHash: 0}
	}
	
	if replyToMsgID != 0 {
		_, err = sender.To(peer).Reply(replyToMsgID).Media(ctx, doc)
	} else {
		_, err = sender.To(peer).Media(ctx, doc)
	}
	return err
}
