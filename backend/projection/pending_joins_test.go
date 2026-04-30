package projection

import "testing"

func TestPendingJoinsRoundTrip(t *testing.T) {
	db := newChannelsDB(t)

	p := PendingJoin{
		InviteHash:  "abc",
		InviteLink:  "https://t.me/+abc",
		Title:       "Goa",
		RequestedAt: 10,
		Status:      PendingJoinStatusPending,
	}
	if err := UpsertPendingJoin(db, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := GetPendingJoin(db, "abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Goa" || got.Status != PendingJoinStatusPending {
		t.Fatalf("got %+v", got)
	}

	if err := UpdatePendingJoinCheck(db, "abc", PendingJoinStatusError, "still waiting"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = GetPendingJoin(db, "abc")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got.Status != PendingJoinStatusError || got.LastError != "still waiting" || got.LastCheckedAt == 0 {
		t.Fatalf("updated got %+v", got)
	}

	list, err := ListPendingJoins(db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	if err := DeletePendingJoin(db, "abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = ListPendingJoins(db)
	if err != nil {
		t.Fatalf("list2: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list len after delete = %d", len(list))
	}
}
