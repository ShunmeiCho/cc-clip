package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shunmei/cc-clip/internal/daemon"
	"github.com/shunmei/cc-clip/internal/session"
	"github.com/shunmei/cc-clip/internal/token"
)

func cmdServe() {
	port := getPort()
	ttl := getTokenTTL()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	rotateToken := hasFlag("rotate-token")

	tm := token.NewManager(ttl)

	var sess token.Session
	var reused bool
	var err error

	if rotateToken {
		sess, err = tm.Generate()
		if err != nil {
			log.Fatalf("failed to generate token: %v", err)
		}
		log.Printf("Token rotated (--rotate-token): new token generated")
	} else {
		sess, reused, err = tm.LoadOrGenerate(ttl)
		if err != nil {
			log.Fatalf("failed to load or generate token: %v", err)
		}
		if reused {
			log.Printf("Token reused from existing file (expires %s)", sess.ExpiresAt.Format(time.RFC3339))
		} else {
			log.Printf("Token generated (no valid existing token found)")
		}
	}

	tokenPath, err := token.WriteTokenFile(sess.Token, sess.ExpiresAt)
	if err != nil {
		log.Fatalf("failed to write token file: %v", err)
	}

	clipboard := daemon.NewClipboardReader()
	store := session.NewStore(12 * time.Hour)
	srv := daemon.NewServer(addr, clipboard, tm, store)
	srv.SetTextWriter(daemon.NewClipboardTextWriter())
	srv.SetVersion(version)
	srv.EnableNoncePersistence()
	if loaded, err := srv.LoadPersistedNonces(); err != nil {
		log.Printf("WARN: failed to load notification nonces: %v", err)
	} else if loaded > 0 {
		log.Printf("Notification nonces restored: %d", loaded)
	}

	log.Printf("Token written to: %s", tokenPath)
	log.Printf("Token expires at: %s", sess.ExpiresAt.Format(time.RFC3339))
	log.Printf("Starting daemon on %s", addr)

	// Start notification delivery, session cleanup, and nonce cleanup in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunNotifier(ctx, daemon.BuildDeliveryChain())
	go store.RunCleanup(ctx, 30*time.Minute)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				srv.CleanupExpiredNonces()
			case <-ctx.Done():
				return
			}
		}
	}()

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
