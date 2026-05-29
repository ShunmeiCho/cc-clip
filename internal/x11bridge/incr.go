package x11bridge

import (
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// IncrTransfer tracks the state of an incremental (INCR) clipboard transfer.
// INCR is required when image data exceeds the X11 maximum request size (~256KB).
//
// Protocol flow:
//  1. Owner writes INCR marker (total size) to requestor's property
//  2. Owner sends SelectionNotify
//  3. Requestor detects type=INCR, deletes property
//  4. Owner detects PropertyDelete, writes first chunk
//  5. Requestor reads chunk, deletes property
//  6. Repeat steps 4-5 until all data sent
//  7. Owner writes zero-length property to signal completion
type IncrTransfer struct {
	Data      []byte
	Offset    int
	FinalSent bool
	ChunkSize int
	Property  xproto.Atom
	Requestor xproto.Window
	Target    xproto.Atom // e.g. image/png atom
}

// IsComplete returns true when all data has been sent (including the final
// zero-length marker).
func (t *IncrTransfer) IsComplete() bool {
	return t.Offset >= len(t.Data) && t.FinalSent
}

// startIncr initiates an INCR transfer by writing the INCR marker to the
// requestor's property and sending SelectionNotify.
func startIncr(
	conn *xgb.Conn,
	event xproto.SelectionRequestEvent,
	atoms *AtomCache,
	data []byte,
	chunkSize int,
) (*IncrTransfer, error) {
	incrAtom := atoms.MustGet(AtomNameIncr)

	// Write INCR marker: a single 32-bit value indicating the data size lower bound.
	sizeBytes := make([]byte, 4)
	xgb.Put32(sizeBytes, uint32(len(data)))

	err := xproto.ChangePropertyChecked(
		conn,
		xproto.PropModeReplace,
		event.Requestor,
		event.Property,
		incrAtom,
		32,
		1,
		sizeBytes,
	).Check()
	if err != nil {
		return nil, err
	}

	// Subscribe to PropertyNotify on the requestor's window so we can
	// detect when the requestor deletes the property (ready for next chunk).
	// If the subscription fails we would never observe PropertyDelete and the
	// transfer would stall until the inactivity timeout, so fail loudly and let
	// the caller refuse the request instead of registering a dead transfer.
	if err := xproto.ChangeWindowAttributesChecked(conn, event.Requestor,
		xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange},
	).Check(); err != nil {
		return nil, err
	}

	if err := sendSelectionNotify(conn, event, event.Property); err != nil {
		return nil, err
	}

	return &IncrTransfer{
		Data:      data,
		Offset:    0,
		ChunkSize: chunkSize,
		Property:  event.Property,
		Requestor: event.Requestor,
		Target:    event.Target,
	}, nil
}

// writeNextChunk writes the next chunk of data (or a zero-length property
// to signal completion) in response to a PropertyDelete event.
func writeNextChunk(conn *xgb.Conn, transfer *IncrTransfer) error {
	var chunk []byte

	if transfer.Offset < len(transfer.Data) {
		end := transfer.Offset + transfer.ChunkSize
		if end > len(transfer.Data) {
			end = len(transfer.Data)
		}
		chunk = transfer.Data[transfer.Offset:end]
		transfer.Offset = end
	} else {
		transfer.FinalSent = true
	}
	// else: chunk is nil (zero-length), signaling completion.

	return xproto.ChangePropertyChecked(
		conn,
		xproto.PropModeReplace,
		transfer.Requestor,
		transfer.Property,
		transfer.Target,
		8,
		uint32(len(chunk)),
		chunk,
	).Check()
}
