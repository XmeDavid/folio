-- Repair environments that applied an earlier draft of the investments
-- migration before realised P/L was added to the materialised position cache.
ALTER TABLE investment_positions
  ADD COLUMN IF NOT EXISTS realised_pnl numeric(28,8) NOT NULL DEFAULT 0;
