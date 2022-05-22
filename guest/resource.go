package guest

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/oklog/ulid"
	"github.com/pkg/errors"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type ProvisionChange struct {
	Id     *guestapi.ResourceId
	Status *guestapi.ProvisionStatus
}

type ResourceContext struct {
	context.Context
	*ResourceStorage
}

type ResourceManager interface {
	Create(ctx *ResourceContext, msg proto.Message) (*guestapi.Resource, error)
	Update(ctx *ResourceContext, res *guestapi.Resource) (*guestapi.Resource, error)
	Read(ctx *ResourceContext, id *guestapi.ResourceId) (*guestapi.Resource, error)
	Delete(ctx *ResourceContext, res *guestapi.Resource) error
}

type ResourceManagerTyped[T proto.Message] interface {
	Create(ctx *ResourceContext, msg T) (*guestapi.Resource, error)
	Update(ctx *ResourceContext, res *guestapi.Resource) (*guestapi.Resource, error)
	Read(ctx *ResourceContext, id *guestapi.ResourceId) (*guestapi.Resource, error)
	Delete(ctx *ResourceContext, res *guestapi.Resource, msg T) error
}

type RMWrapper[T proto.Message] struct {
	ResourceManagerTyped[T]
}

func (m *RMWrapper[T]) Create(ctx *ResourceContext, msg proto.Message) (*guestapi.Resource, error) {
	target, ok := msg.(T)
	if !ok {
		return nil, ErrInvalidResource
	}

	return m.ResourceManagerTyped.Create(ctx, target)
}

func (m *RMWrapper[T]) Delete(ctx *ResourceContext, res *guestapi.Resource) error {
	msg, err := res.Resource.UnmarshalNew()
	if err != nil {
		return err
	}

	target, ok := msg.(T)
	if !ok {
		return ErrInvalidResource
	}

	return m.ResourceManagerTyped.Delete(ctx, res, target)
}

func TypedManager[T proto.Message](rm ResourceManagerTyped[T]) ResourceManager {
	return &RMWrapper[T]{
		ResourceManagerTyped: rm,
	}
}

type ResourceRegistry struct {
	mu  sync.Mutex
	reg map[string]ResourceManager
}

func (r *ResourceRegistry) Register(msg proto.Message, mgr ResourceManager) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := string(msg.ProtoReflect().Type().Descriptor().FullName())

	if r.reg == nil {
		r.reg = make(map[string]ResourceManager)
	}

	r.reg[name] = mgr
}

func (r *ResourceRegistry) Manager(msg proto.Message) (ResourceManager, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := string(msg.ProtoReflect().Type().Descriptor().FullName())

	mgr, ok := r.reg[name]

	return mgr, ok
}

var DefaultRegistry ResourceRegistry

var reader = ulid.Monotonic(rand.Reader, 0)

func NewId(category, typ string) *guestapi.ResourceId {
	ts, err := ulid.New(ulid.Now(), reader)
	if err != nil {
		panic(err)
	}

	return &guestapi.ResourceId{
		Category: category,
		Type:     typ,
		UniqueId: ts[:],
	}
}

type ResourceIndices struct {
	Fields  protoreflect.FieldDescriptors
	Indices []int32
	Descs   []protoreflect.FieldDescriptor
}

type ResourceStorage struct {
	log hclog.Logger
	db  *bbolt.DB

	schema map[string]*ResourceIndices

	watchers map[string][]chan struct{}

	provChange *Signal[ProvisionChange]
}

func (r *ResourceStorage) ProvisionChangeSelector() *Signal[ProvisionChange] {
	return r.provChange
}

type Schema struct {
	Category string
	Type     string
	Indexes  map[string]int32
}

func (s *Schema) Key(name string, val interface{}) (*guestapi.ResourceIndexKey, error) {
	field, ok := s.Indexes[name]
	if !ok {
		return nil, errors.Wrapf(ErrUnknownKey, "unknown index: %s", name)
	}

	sval, err := structpb.NewValue(val)
	if err != nil {
		return nil, err
	}

	return &guestapi.ResourceIndexKey{
		Category: s.Category,
		Type:     s.Type,
		Field:    field,
		Value:    sval,
	}, nil
}

func (s *Schema) NewId() *guestapi.ResourceId {
	return NewId(s.Category, s.Type)
}

func (r *ResourceStorage) SetSchema(category, typ string, msg proto.Message, fields ...string) (*Schema, error) {
	fieldDesc := msg.ProtoReflect().Type().Descriptor().Fields()

	var (
		intfields []int32
		descs     []protoreflect.FieldDescriptor
	)

	sch := &Schema{
		Category: category,
		Type:     typ,
		Indexes:  make(map[string]int32),
	}

	for _, name := range fields {
		fd := fieldDesc.ByJSONName(name)
		if fd == nil {
			fd = fieldDesc.ByTextName(name)
		}

		if fd == nil {
			return nil, errors.Wrapf(ErrUnknownKey, "unknown field: %s", name)
		}

		idx := int32(fd.Index())

		sch.Indexes[name] = int32(fd.Number())

		intfields = append(intfields, idx)
		descs = append(descs, fd)
	}

	if r.schema == nil {
		r.schema = map[string]*ResourceIndices{}
	}

	r.schema[category+"/"+typ] = &ResourceIndices{
		Fields:  fieldDesc,
		Indices: intfields,
		Descs:   descs,
	}

	return sch, nil
}

func (r *ResourceStorage) Init(db *bbolt.DB) error {
	r.db = db
	r.schema = make(map[string]*ResourceIndices)
	r.watchers = make(map[string][]chan struct{})

	r.provChange = NewSignal[ProvisionChange]()

	return r.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(resBucket)
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists(resIndexBucket)
		return err
	})
}

func (r *ResourceStorage) idToBytes(id *guestapi.ResourceId) []byte {
	return []byte(fmt.Sprintf("%s/%s/%s", id.Category, id.Type, id.UniqueId))
}

var (
	resBucket      = []byte("resources")
	resIndexBucket = []byte("resources_index")
)

var (
	ErrNotFound        = errors.New("not found")
	ErrInvalidResource = errors.New("invalid resource detected")
	ErrImmutable       = errors.New("resource is immutable")
	ErrConflict        = errors.New("conflicting resource detected")
)

func (r *ResourceStorage) Fetch(id *guestapi.ResourceId) (*guestapi.Resource, error) {
	var res guestapi.Resource

	err := r.db.View(func(tx *bbolt.Tx) error {
		data := tx.Bucket(resBucket).Get(r.idToBytes(id))
		if data == nil {
			return errors.Wrapf(ErrNotFound, "unable to find id: %s", id.String())
		}

		return proto.Unmarshal(data, &res)
	})
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func FetchAs[T proto.Message](r *ResourceContext, id *guestapi.ResourceId, val T) (*guestapi.Resource, error) {
	res, err := r.Fetch(id)
	if err != nil {
		return nil, err
	}

	err = res.Resource.UnmarshalTo(val)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (r *ResourceStorage) FetchByIndex(key *guestapi.ResourceIndexKey) ([]*guestapi.Resource, error) {
	var resources []*guestapi.Resource

	err := r.db.View(func(tx *bbolt.Tx) error {
		dataKey, err := proto.Marshal(key)
		if err != nil {
			return err
		}

		bucket := tx.Bucket(resIndexBucket)

		cur := bucket.Cursor()

		tKey, tData := cur.Seek(dataKey)
		if tData == nil {
			return errors.Wrapf(ErrNotFound, "unable to find key")
		}

		for tKey != nil {
			var testKey guestapi.ResourceIndexKey

			err = proto.Unmarshal(tKey, &testKey)
			if err != nil {
				return err
			}

			if testKey.Category != key.Category || testKey.Type != key.Type || testKey.Field != key.Field || !proto.Equal(testKey.Value, key.Value) {
				break
			}

			valData := tx.Bucket(resBucket).Get(tData)
			if valData == nil {
				return errors.Wrapf(ErrNotFound, "corrupt index, key for unknown id")
			}

			var res guestapi.Resource

			err = proto.Unmarshal(valData, &res)
			if err != nil {
				return err
			}

			resources = append(resources, &res)

			tKey, tData = cur.Next()
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return resources, nil
}

func (r *ResourceStorage) Set(ctx context.Context, id *guestapi.ResourceId, msg proto.Message, prov *guestapi.ProvisionStatus) (*guestapi.Resource, error) {
	resAny, err := anypb.New(msg)
	if err != nil {
		return nil, err
	}

	res := &guestapi.Resource{
		Id:              id,
		Resource:        resAny,
		ProvisionStatus: prov,
	}

	err = r.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(resBucket)
		key := r.idToBytes(id)

		data, err := proto.Marshal(res)
		if err != nil {
			return err
		}

		existingData := bucket.Get(key)

		err = bucket.Put(key, data)
		if err != nil {
			return err
		}

		existingRes := &guestapi.Resource{}
		existing := msg.ProtoReflect().New()

		var update bool
		if existingData != nil {
			update = true

			err := proto.Unmarshal(existingData, existingRes)
			if err != nil {
				return errors.Wrapf(err, "attempting to unmarshaling existing entry for key")
			}

			existingRes.Resource.UnmarshalTo(existing.Interface())

			if !proto.Equal(prov, existingRes.ProvisionStatus) {
				defer func() {
					go r.provChange.Emit(ctx, id.Short(), ProvisionChange{
						Id:     id,
						Status: prov,
					})
				}()
			}
		}

		indices := r.schema[id.Category+"/"+id.Type]

		if indices != nil {
			r.log.Trace("detected indexes", "category", id.Category, "type", id.Type)

			idxBucket := tx.Bucket(resIndexBucket)

			for _, desc := range indices.Descs {
				val := msg.ProtoReflect().Get(desc)

				valI := val.Interface()

				if update {
					curVal := existing.Interface().ProtoReflect().Get(desc)

					curValI := curVal.Interface()
					if valI == curValI {
						continue
					} else {
						sval, err := structpb.NewValue(curValI)
						if err != nil {
							return errors.Wrapf(err, "attempting to convert field to index key: %s", desc.Name())
						}

						dkey := &guestapi.ResourceIndexKey{
							Category: id.Category,
							Type:     id.Type,
							Field:    int32(desc.Number()),
							Value:    sval,
							UniqueId: id.UniqueId,
						}

						dataKey, err := proto.Marshal(dkey)
						if err != nil {
							return err
						}

						err = idxBucket.Delete(dataKey)
						if err != nil {
							return errors.Wrapf(err, "attempting to delete old index value: %s", desc.Name())
						}
					}
				}

				sval, err := structpb.NewValue(valI)
				if err != nil {
					return errors.Wrapf(err, "attempting to convert field to index key: %s", desc.Name())
				}

				dkey := &guestapi.ResourceIndexKey{
					Category: id.Category,
					Type:     id.Type,
					Field:    int32(desc.Number()),
					Value:    sval,
					UniqueId: id.UniqueId,
				}

				r.log.Trace("storing key for index", "index-name", desc.Name(), "value", valI)

				dataKey, err := proto.Marshal(dkey)
				if err != nil {
					return err
				}

				err = idxBucket.Put(dataKey, key)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (r *ResourceStorage) indexKeys(id *guestapi.ResourceId, msg proto.Message, fn func(key []byte) error) error {
	indices := r.schema[id.Category+"/"+id.Type]
	if indices == nil {
		return nil
	}

	mo := proto.MarshalOptions{
		Deterministic: true,
	}

	buf := make([]byte, 1024)

	fields := indices.Fields
	for _, fieldIdx := range indices.Indices {
		desc := fields.ByNumber(protowire.Number(fieldIdx))
		if desc == nil {
			return errors.Wrapf(ErrNotFound, "index references unknown field: %d", fieldIdx)
		}

		val := msg.ProtoReflect().Get(desc)

		valI := val.Interface()

		sval, err := structpb.NewValue(valI)
		if err != nil {
			return errors.Wrapf(err, "attempting to convert field to index key: %d", fieldIdx)
		}

		dkey := &guestapi.ResourceIndexKey{
			Category: id.Category,
			Type:     id.Type,
			Field:    fieldIdx,
			Value:    sval,
			UniqueId: id.UniqueId,
		}

		dataKey, err := mo.MarshalAppend(buf[:0], dkey)
		if err != nil {
			return err
		}

		err = fn(dataKey)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ResourceStorage) Delete(ctx context.Context, id *guestapi.ResourceId) (*guestapi.Resource, error) {
	var res guestapi.Resource

	err := r.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(resBucket)
		key := r.idToBytes(id)

		data := bucket.Get(key)
		if data == nil {
			return errors.Wrapf(ErrNotFound, "unable to find id: %s", id.String())
		}

		err := proto.Unmarshal(data, &res)
		if err != nil {
			return err
		}

		idxBucket := tx.Bucket(resIndexBucket)

		msg, err := res.Resource.UnmarshalNew()
		if err != nil {
			return errors.Wrapf(err, "attempting to read existing value for index cleanup")
		}

		r.indexKeys(id, msg, func(key []byte) error {
			return idxBucket.Delete(key)
		})

		return bucket.Delete(key)
	})
	if err != nil {
		return nil, err
	}

	go r.provChange.Emit(ctx, id.Short(), ProvisionChange{
		Id: id,
	})

	return &res, nil
}

func (r *ResourceStorage) SetError(ctx context.Context, id *guestapi.ResourceId, ierr error) error {
	var res guestapi.Resource

	err := r.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(resBucket)
		key := r.idToBytes(id)

		data := bucket.Get(key)
		if data == nil {
			return errors.Wrapf(ErrNotFound, "unable to find id: %s", id.String())
		}

		err := proto.Unmarshal(data, &res)
		if err != nil {
			return err
		}

		res.ProvisionStatus.Status = guestapi.ProvisionStatus_DEAD
		res.ProvisionStatus.LastError = ierr.Error()

		data, err = proto.Marshal(&res)
		if err != nil {
			return err
		}

		return bucket.Put(key, data)
	})
	if err != nil {
		return err
	}

	go r.provChange.Emit(ctx, id.Short(), ProvisionChange{
		Id:     id,
		Status: res.ProvisionStatus,
	})

	return nil
}

func (r *ResourceStorage) WatchProvision(id *guestapi.ResourceId) (*guestapi.ProvisionStatus, chan struct{}, error) {
	var res guestapi.Resource

	ch := make(chan struct{})

	err := r.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(resBucket)
		key := r.idToBytes(id)

		data := bucket.Get(key)
		if data == nil {
			return errors.Wrapf(ErrNotFound, "unable to find id: %s", id.String())
		}

		err := proto.Unmarshal(data, &res)
		if err != nil {
			return err
		}

		r.watchers[id.Short()] = append(r.watchers[id.Short()], ch)

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return res.ProvisionStatus, ch, nil
}

func (r *ResourceStorage) UpdateProvision(ctx context.Context, id *guestapi.ResourceId, prov *guestapi.ProvisionStatus) error {
	var res guestapi.Resource

	err := r.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(resBucket)
		key := r.idToBytes(id)

		data := bucket.Get(key)
		if data == nil {
			return errors.Wrapf(ErrNotFound, "unable to find id: %s", id.String())
		}

		err := proto.Unmarshal(data, &res)
		if err != nil {
			return err
		}

		if prov.Status != guestapi.ProvisionStatus_UNKNOWN {
			res.ProvisionStatus.Status = prov.Status
		}

		if prov.LastError != "" {
			res.ProvisionStatus.LastError = prov.LastError
		}

		if prov.ContainerInfo != nil {
			res.ProvisionStatus.ContainerInfo = prov.ContainerInfo
		}

		if prov.NetworkInfo != nil {
			res.ProvisionStatus.NetworkInfo = prov.NetworkInfo
		}

		data, err = proto.Marshal(&res)
		if err != nil {
			return err
		}

		err = bucket.Put(key, data)
		if err != nil {
			return err
		}

		waiters := r.watchers[id.Short()]
		delete(r.watchers, id.Short())

		for _, ch := range waiters {
			close(ch)
		}

		return nil
	})
	if err != nil {
		return err
	}

	go r.provChange.Emit(ctx, id.Short(), ProvisionChange{
		Id:     id,
		Status: prov,
	})

	return nil
}
