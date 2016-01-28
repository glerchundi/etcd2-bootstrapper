// Copyright 2016 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clientv3

type opType int

const (
	// A default Op has opType 0, which is invalid.
	tRange opType = iota + 1
	tPut
	tDeleteRange
)

// Op represents an Operation that kv can execute.
type Op struct {
	t   opType
	key []byte
	end []byte

	// for range
	limit int64
	rev   int64
	sort  *SortOption
}

func OpRange(key, end string, limit, rev int64, sort *SortOption) Op {
	return Op{
		t:   tRange,
		key: []byte(key),
		end: []byte(end),

		limit: limit,
		rev:   rev,
		sort:  sort,
	}
}

func OpGet(key string, rev int64) Op {
	return Op{
		t:   tRange,
		key: []byte(key),

		rev: rev,
	}
}

func OpDeleteRange(key, end string) Op {
	return Op{
		t:   tDeleteRange,
		key: []byte(key),
		end: []byte(end),
	}
}

func OpDelete(key string) Op {
	return Op{
		t:   tDeleteRange,
		key: []byte(key),
	}
}
