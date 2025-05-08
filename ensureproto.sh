#!/bin/bash
protoc --go_out=. --go_opt=paths=source_relative internal/protos/SophonManifestProto.proto
protoc --go_out=. --go_opt=paths=source_relative internal/protos/SophonPatchProto.proto
