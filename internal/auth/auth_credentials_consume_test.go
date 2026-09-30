package auth

import (
	"sync"
	"testing"
	"time"
)

// TestConsumeToken_ConcurrentRedemptionSingleWinner pins the atomic single-use
// guarantee for reset/verify/invite tokens: when two consumers race on the same
// token, exactly ONE may win. The redemption must be a single conditional
// UPDATE (used_at IS NULL) — a check-then-update sequence lets both SELECTs see
// the unused row before either commits its UPDATE, so both succeed.
func TestConsumeToken_ConcurrentRedemptionSingleWinner(t *testing.T) {
	s := newTestStore(t)

	const attempts = 40
	for i := 0; i < attempts; i++ {
		plain, err := s.CreateToken(TokenResetPassword, 0, "race@test.com", time.Hour)
		if err != nil {
			t.Fatalf("CreateToken: %v", err)
		}
		start := make(chan struct{}) // barrier: release both goroutines together
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				<-start
				_, errs[g] = s.ConsumeToken(plain, TokenResetPassword)
			}(g)
		}
		close(start)
		wg.Wait()

		winners := 0
		for _, err := range errs {
			if err == nil {
				winners++
			}
		}
		if winners > 1 {
			t.Fatalf("attempt %d: both concurrent redemptions of the same token succeeded (%d winners) — ConsumeToken is not atomic single-use", i, winners)
		}
	}
}
