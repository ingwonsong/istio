package entrypoint

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"google.golang.org/protobuf/proto"
)

func ReadProto(protoInstance interface{}) error {
	b, err := os.ReadFile(ProtoFile)
	if err != nil {
		return fmt.Errorf("cannot read file %q: %v", ProtoFile, err)
	}
	if protoInstance == nil || reflect.ValueOf(protoInstance).IsNil() {
		return errors.New("Memory for protobuf was not passed to ReadProto. Make sure the protoInstance passed to ReadProto is assigned to an empty instance of a protobuf type.")
	}
	pbForUnmarshal, isProtoMessage := protoInstance.(proto.Message)
	if !isProtoMessage {
		return errors.New("Memory allocated for the protobuf instance storing the parsed textpb was malformed. Make sure protoInstance passed to ReadProto is a proper protobuf type.")
	}
	if err := proto.Unmarshal(b, pbForUnmarshal); err != nil {
		return fmt.Errorf("failed to unmarshal protobuf from %s: %q", ProtoFile, err)
	}
	return nil
}
