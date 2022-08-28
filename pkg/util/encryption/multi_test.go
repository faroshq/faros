package encryption

import (
	"errors"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"

	mock_encryption "github.com/faroshq/faros/pkg/util/mocks/encryption"
)

func TestOpen(t *testing.T) {
	mockInput := "fakeInput"

	type test struct {
		name       string
		mocks      func(firstOpener *mock_encryption.MockAEAD, secondOpener *mock_encryption.MockAEAD)
		wantResult []byte
		wantErr    string
	}

	for _, tt := range []*test{
		{
			name: "first opener succeeds, do not try second",
			mocks: func(firstOpener *mock_encryption.MockAEAD, secondOpener *mock_encryption.MockAEAD) {
				firstOpener.EXPECT().Open(mockInput).Return("result from the first opener", nil)
			},
			wantResult: []byte("result from the first opener"),
		},
		{
			name: "first opener errors, but second succeeds",
			mocks: func(firstOpener *mock_encryption.MockAEAD, secondOpener *mock_encryption.MockAEAD) {
				firstOpener.EXPECT().Open(mockInput).Return(nil, errors.New("fake error from the first opener"))
				secondOpener.EXPECT().Open(mockInput).Return("result from the second opener", nil)
			},
			wantResult: []byte("result from the second opener"),
		},
		{
			name: "all openers error",
			mocks: func(firstOpener *mock_encryption.MockAEAD, secondOpener *mock_encryption.MockAEAD) {
				firstOpener.EXPECT().Open(mockInput).Return(nil, errors.New("fake error from the first opener"))
				secondOpener.EXPECT().Open(mockInput).Return(nil, errors.New("fake error from the second opener"))
			},
			wantErr: "fake error from the second opener",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			defer controller.Finish()

			firstOpener := mock_encryption.NewMockAEAD(controller)
			secondOpener := mock_encryption.NewMockAEAD(controller)

			multi := multi{
				openers: []AEAD{
					firstOpener,
					secondOpener,
				},
			}

			tt.mocks(firstOpener, secondOpener)

			b, err := multi.Open(mockInput)
			if err != nil && err.Error() != tt.wantErr ||
				err == nil && tt.wantErr != "" {
				t.Error(err)
			}
			if b != "" && !reflect.DeepEqual(tt.wantResult, b) ||
				b == "" && tt.wantResult != nil {
				t.Error(b)
			}
		})
	}
}
