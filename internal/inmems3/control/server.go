package control

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/hyscale-lab/rdma-demo/internal/inmems3/payload"
	controlv1 "github.com/hyscale-lab/rdma-demo/proto/inmems3/control/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Loader interface {
	AddFolder(ctx context.Context, root string) (payload.AddFolderResult, error)
	DeleteBucket(name string) bool
	ClearAll() int
	ListBuckets() []string
}

type Server struct {
	controlv1.UnimplementedControlServiceServer
	loader Loader
}

func NewServer(loader Loader) *Server {
	return &Server{loader: loader}
}

func RegisterGRPC(grpcServer *grpc.Server, loader Loader) {
	controlv1.RegisterControlServiceServer(grpcServer, NewServer(loader))
}

func (s *Server) AddFolder(ctx context.Context, req *controlv1.AddFolderRequest) (*controlv1.AddFolderResponse, error) {
	if strings.TrimSpace(req.GetPath()) == "" {
		return nil, status.Error(codes.InvalidArgument, "path must not be empty")
	}

	result, err := s.loader.AddFolder(ctx, req.GetPath())
	if err != nil {
		return nil, toStatusError(err)
	}

	return &controlv1.AddFolderResponse{
		BucketsLoaded: uint32(result.BucketsLoaded),
		ObjectsLoaded: uint32(result.ObjectsLoaded),
		BytesLoaded:   uint64(result.BytesLoaded),
		Buckets:       result.Buckets,
	}, nil
}

func (s *Server) DeleteBucket(_ context.Context, req *controlv1.DeleteBucketRequest) (*controlv1.DeleteBucketResponse, error) {
	bucket := strings.TrimSpace(req.GetBucket())
	if bucket == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket must not be empty")
	}
	return &controlv1.DeleteBucketResponse{
		Deleted: s.loader.DeleteBucket(bucket),
	}, nil
}

func (s *Server) ClearAll(context.Context, *controlv1.ClearAllRequest) (*controlv1.ClearAllResponse, error) {
	return &controlv1.ClearAllResponse{
		BucketsDeleted: uint32(s.loader.ClearAll()),
	}, nil
}

func (s *Server) ListBuckets(context.Context, *controlv1.ListBucketsRequest) (*controlv1.ListBucketsResponse, error) {
	return &controlv1.ListBucketsResponse{
		Buckets: s.loader.ListBuckets(),
	}, nil
}

func toStatusError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return status.Error(codes.NotFound, err.Error())
	case strings.Contains(err.Error(), "is not a directory"):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
