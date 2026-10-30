package atrepo

import (
	"bytes"
	"context"
	"encoding/binary"
	"sort"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/events"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/multiformats/go-multihash"
	cbg "github.com/whyrusleeping/cbor-gen"
)

// cborCid is the CIDv1 dag-cbor sha2-256 address atproto uses for every block.
func cborCid(data []byte) (cid.Cid, error) {
	return cid.NewPrefixV1(cid.DagCBOR, multihash.SHA2_256).Sum(data)
}

// encodeCAR writes a CARv1 rooted at root holding blocks.
func encodeCAR(root cid.Cid, rows []blockRow) ([]byte, error) {
	header, err := cbor.DumpObject(map[string]any{"roots": []cid.Cid{root}, "version": 1})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeSection(&buf, header)
	for _, b := range rows {
		c, err := cid.Decode(b.Cid)
		if err != nil {
			return nil, err
		}
		writeSection(&buf, append(c.Bytes(), b.Data...))
	}
	return buf.Bytes(), nil
}

func writeSection(buf *bytes.Buffer, data []byte) {
	var n [binary.MaxVarintLen64]byte
	buf.Write(n[:binary.PutUvarint(n[:], uint64(len(data)))])
	buf.Write(data)
}

// encodeFrame renders one subscribeRepos message: a {op, t} header followed by
// the body, both DAG-CBOR.
func encodeFrame(msgType string, body cbg.CBORMarshaler) ([]byte, error) {
	var buf bytes.Buffer
	header := events.EventHeader{Op: events.EvtKindMessage, MsgType: msgType}
	if err := header.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	if err := body.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ErrorFrame renders a terminal subscribeRepos error frame (op -1).
func ErrorFrame(name, message string) ([]byte, error) {
	var buf bytes.Buffer
	header := events.EventHeader{Op: events.EvtKindErrorFrame}
	if err := header.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	body := events.ErrorFrame{Error: name, Message: message}
	if err := body.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func identityFrame(did string, seq int64, now string) (eventRow, error) {
	frame, err := encodeFrame("#identity", &comatproto.SyncSubscribeRepos_Identity{Did: did, Seq: seq, Time: now})
	return eventRow{Seq: seq, Did: did, Frame: frame}, err
}

func accountFrame(did string, active bool, status string, seq int64, now string) (eventRow, error) {
	evt := &comatproto.SyncSubscribeRepos_Account{Did: did, Active: active, Seq: seq, Time: now}
	if !active {
		evt.Status = &status
	}
	frame, err := encodeFrame("#account", evt)
	return eventRow{Seq: seq, Did: did, Frame: frame}, err
}

// memBlockstore is the minimal blockstore.Blockstore mst.WriteDiffBlocks
// needs; it only ever holds one commit's freshly encoded nodes.
type memBlockstore struct {
	blocks map[cid.Cid]blocks.Block
}

func newMemBlockstore() *memBlockstore {
	return &memBlockstore{blocks: map[cid.Cid]blocks.Block{}}
}

func (m *memBlockstore) sorted() []blocks.Block {
	out := make([]blocks.Block, 0, len(m.blocks))
	for _, b := range m.blocks {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cid().KeyString() < out[j].Cid().KeyString() })
	return out
}

func (m *memBlockstore) DeleteBlock(_ context.Context, c cid.Cid) error {
	delete(m.blocks, c)
	return nil
}

func (m *memBlockstore) Has(_ context.Context, c cid.Cid) (bool, error) {
	_, ok := m.blocks[c]
	return ok, nil
}

func (m *memBlockstore) Get(_ context.Context, c cid.Cid) (blocks.Block, error) {
	if b, ok := m.blocks[c]; ok {
		return b, nil
	}
	return nil, ipld.ErrNotFound{Cid: c}
}

func (m *memBlockstore) GetSize(ctx context.Context, c cid.Cid) (int, error) {
	b, err := m.Get(ctx, c)
	if err != nil {
		return 0, err
	}
	return len(b.RawData()), nil
}

func (m *memBlockstore) Put(_ context.Context, b blocks.Block) error {
	m.blocks[b.Cid()] = b
	return nil
}

func (m *memBlockstore) PutMany(ctx context.Context, bs []blocks.Block) error {
	for _, b := range bs {
		m.blocks[b.Cid()] = b
	}
	return nil
}

func (m *memBlockstore) AllKeysChan(context.Context) (<-chan cid.Cid, error) {
	ch := make(chan cid.Cid, len(m.blocks))
	for c := range m.blocks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (m *memBlockstore) HashOnRead(bool) {}
