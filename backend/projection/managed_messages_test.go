package projection

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestManagedMsgIDsInIncludesHiddenBodiesAndScopesCandidates(t *testing.T) {
	db := newTestDB(t)
	statements := []string{
		`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,tombstoned) VALUES(1001,10,'live.bin',1,'',0,11,0),(1001,20,'trash.bin',1,'',0,21,1),(888888,99,'other.bin',1,'',0,99,0)`,
		`INSERT INTO file_revisions(channel_id,file_msg_id,revision,content_msg_id,committed_msg_id) VALUES(1001,10,1,12,10)`,
		`INSERT INTO file_parts(channel_id,upload_uuid,part_index,msg_id,size) VALUES(1001,'parts',0,30,1)`,
		`INSERT INTO file_renditions(channel_id,msg_id,file_msg_id,kind,version,size,plaintext_size,width,height,encrypted) VALUES(1001,40,10,'thumbnail',1,1,1,1,1,0)`,
		`INSERT INTO replay_log(channel_id,msg_id,op_type,op_payload_json,raw_header,first_seen_hash,seen_at) VALUES(1001,50,'unknown','{}','TDX1|t=unknown','hash',0)`,
		`INSERT INTO pending_part_cleanup(channel_id,msg_id) VALUES(1001,60)`,
		`INSERT INTO hard_delete_plan_items(channel_id,op_id,msg_id) VALUES(1001,'deleted-parts',70)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed ownership: %v", err)
		}
	}
	ids, err := ManagedMsgIDsIn(t.Context(), db, testChan, []int64{10, 11, 12, 20, 21, 30, 40, 50, 60, 70, 99, 100, 10})
	if err != nil {
		t.Fatalf("managed IDs: %v", err)
	}
	got := slices.Sorted(maps.Keys(ids))
	want := []int64{10, 11, 12, 20, 21, 30, 40, 50, 60, 70}
	if !slices.Equal(got, want) {
		t.Fatalf("managed IDs = %v, want %v", got, want)
	}
	ids, err = ManagedMsgIDsIn(t.Context(), db, testChan, []int64{11})
	if err != nil || len(ids) != 1 {
		t.Fatalf("single candidate = %v, %v", ids, err)
	}
	if _, ok := ids[11]; !ok {
		t.Fatalf("single candidate returned unrelated IDs: %v", ids)
	}
}

func TestManagedMsgIDsInBatchesAndHonorsCancellation(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`INSERT INTO file_parts(channel_id,upload_uuid,part_index,msg_id) VALUES(1001,'part',0,1500)`); err != nil {
		t.Fatal(err)
	}
	candidates := make([]int64, 1500)
	for i := range candidates {
		candidates[i] = int64(i + 1)
	}
	ids, err := ManagedMsgIDsIn(t.Context(), db, testChan, candidates)
	if err != nil || len(ids) != 1 {
		t.Fatalf("batched IDs = %v, %v", ids, err)
	}
	if _, ok := ids[1500]; !ok {
		t.Fatal("last batch omitted owned part")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ManagedMsgIDsIn(ctx, db, testChan, candidates); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}

func TestManagedMessageContentAndCleanupLookupsUseIndexes(t *testing.T) {
	db := newTestDB(t)
	for _, lookup := range []struct{ table, column, index string }{
		{"files", "content_msg_id", "idx_files_content_msg"},
		{"hard_delete_plan_items", "msg_id", "idx_hard_delete_plan_items_msg"},
	} {
		t.Run(lookup.table, func(t *testing.T) {
			rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT ` + lookup.column + ` FROM ` + lookup.table + ` WHERE channel_id=1001 AND ` + lookup.column + `>0 AND ` + lookup.column + ` IN (10,11)`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			used := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				used = used || strings.Contains(detail, lookup.index)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !used {
				t.Fatalf("ownership lookup did not use %s", lookup.index)
			}
		})
	}
}
