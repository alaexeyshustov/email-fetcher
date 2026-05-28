package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/fanout"
)

// EmailServer implements the gRPC EmailServiceServer interface.
type EmailServer struct {
	emailv1.UnimplementedEmailServiceServer
	fanout *fanout.Fanout
}

func New(f *fanout.Fanout) *EmailServer {
	return &EmailServer{fanout: f}
}

func (s *EmailServer) ListEmails(req *emailv1.ListEmailsRequest, stream grpc.ServerStreamingServer[emailv1.ListEmailsResponse]) error {
	results := s.fanout.ListEmails(stream.Context(), req.GetCredentials(), req.GetFormat(), req.GetMaxResults(), req.GetPageToken(), req.GetLabelIds())

	nextTokens := make(map[string]string)
	var failedProviders, failedErrors []string
	var totalCount int32

	for r := range results {
		if r.Err != nil {
			failedProviders = append(failedProviders, r.ProviderName)
			failedErrors = append(failedErrors, r.Err.Error())
			continue
		}
		for _, e := range r.Emails {
			if err := stream.Send(&emailv1.ListEmailsResponse{
				Payload: &emailv1.ListEmailsResponse_Email{Email: e},
			}); err != nil {
				return err
			}
			totalCount++
		}
		if r.NextPageToken != "" {
			nextTokens[r.ProviderName] = r.NextPageToken
		}
	}

	if len(failedProviders) > 0 {
		stream.SetTrailer(metadata.Pairs(
			"x-failed-providers", strings.Join(failedProviders, ","),
			"x-provider-errors", strings.Join(failedErrors, "|"),
		))
	}

	return stream.Send(&emailv1.ListEmailsResponse{
		Payload: &emailv1.ListEmailsResponse_PageInfo{
			PageInfo: &emailv1.PageInfo{
				NextPageToken: fanout.BuildCompositeToken(nextTokens),
				ResultCount:   totalCount,
			},
		},
	})
}

func (s *EmailServer) SearchEmails(req *emailv1.SearchEmailsRequest, stream grpc.ServerStreamingServer[emailv1.SearchEmailsResponse]) error {
	results := s.fanout.SearchEmails(stream.Context(), req.GetCredentials(), req.GetQuery(), req.GetFormat(), req.GetMaxResults(), req.GetPageToken())

	nextTokens := make(map[string]string)
	var failedProviders, failedErrors []string
	var totalCount int32

	for r := range results {
		if r.Err != nil {
			failedProviders = append(failedProviders, r.ProviderName)
			failedErrors = append(failedErrors, r.Err.Error())
			continue
		}
		for _, e := range r.Emails {
			if err := stream.Send(&emailv1.SearchEmailsResponse{
				Payload: &emailv1.SearchEmailsResponse_Email{Email: e},
			}); err != nil {
				return err
			}
			totalCount++
		}
		if r.NextPageToken != "" {
			nextTokens[r.ProviderName] = r.NextPageToken
		}
	}

	if len(failedProviders) > 0 {
		stream.SetTrailer(metadata.Pairs(
			"x-failed-providers", strings.Join(failedProviders, ","),
			"x-provider-errors", strings.Join(failedErrors, "|"),
		))
	}

	return stream.Send(&emailv1.SearchEmailsResponse{
		Payload: &emailv1.SearchEmailsResponse_PageInfo{
			PageInfo: &emailv1.PageInfo{
				NextPageToken: fanout.BuildCompositeToken(nextTokens),
				ResultCount:   totalCount,
			},
		},
	})
}

func (s *EmailServer) GetEmail(ctx context.Context, req *emailv1.GetEmailRequest) (*emailv1.GetEmailResponse, error) {
	prov, err := s.fanout.Lookup(req.GetCredentials().GetProvider())
	if err != nil {
		return nil, err
	}
	email, err := prov.GetEmail(ctx, req.GetCredentials().GetAccessToken(), req.GetId(), req.GetFormat())
	if err != nil {
		return nil, err
	}
	return &emailv1.GetEmailResponse{Email: email}, nil
}

func (s *EmailServer) GetLabels(ctx context.Context, req *emailv1.GetLabelsRequest) (*emailv1.GetLabelsResponse, error) {
	prov, err := s.fanout.Lookup(req.GetCredentials().GetProvider())
	if err != nil {
		return nil, err
	}
	labels, err := prov.GetLabels(ctx, req.GetCredentials().GetAccessToken())
	if err != nil {
		return nil, err
	}
	return &emailv1.GetLabelsResponse{Labels: labels}, nil
}

func (s *EmailServer) GetUnreadCount(ctx context.Context, req *emailv1.GetUnreadCountRequest) (*emailv1.GetUnreadCountResponse, error) {
	prov, err := s.fanout.Lookup(req.GetCredentials().GetProvider())
	if err != nil {
		return nil, err
	}
	count, err := prov.GetUnreadCount(ctx, req.GetCredentials().GetAccessToken(), req.GetLabelId())
	if err != nil {
		return nil, err
	}
	return &emailv1.GetUnreadCountResponse{Count: count}, nil
}

func (s *EmailServer) ModifyLabels(ctx context.Context, req *emailv1.ModifyLabelsRequest) (*emailv1.ModifyLabelsResponse, error) {
	prov, err := s.fanout.Lookup(req.GetCredentials().GetProvider())
	if err != nil {
		return nil, err
	}
	if err := prov.ModifyLabels(ctx, req.GetCredentials().GetAccessToken(), req.GetMessageId(), req.GetAddLabelIds(), req.GetRemoveLabelIds()); err != nil {
		return nil, err
	}
	return &emailv1.ModifyLabelsResponse{}, nil
}

func (s *EmailServer) GetAttachmentContent(ctx context.Context, req *emailv1.GetAttachmentContentRequest) (*emailv1.GetAttachmentContentResponse, error) {
	prov, err := s.fanout.Lookup(req.GetCredentials().GetProvider())
	if err != nil {
		return nil, err
	}
	content, err := prov.GetAttachmentContent(ctx, req.GetCredentials().GetAccessToken(), req.GetMessageId(), req.GetAttachmentId())
	if err != nil {
		return nil, err
	}
	return &emailv1.GetAttachmentContentResponse{Content: content}, nil
}
