// Package plugin implements the sqlc codegen plugin. It converts sqlc's
// GenerateRequest (catalog and analysed queries) into a manifest file; it
// performs no file system access, so it runs unchanged as a process plugin
// or compiled to WASM.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"

	"github.com/Xwudao/go-sqlcx/internal/manifest"
)

// Generate handles one sqlc codegen request.
func Generate(_ context.Context, req *pb.GenerateRequest) (*pb.GenerateResponse, error) {
	m, err := Build(req)
	if err != nil {
		return nil, err
	}
	b, err := m.Marshal()
	if err != nil {
		return nil, err
	}
	return &pb.GenerateResponse{Files: []*pb.File{{Name: manifest.FileName, Contents: b}}}, nil
}

// Build converts a request into a manifest.
func Build(req *pb.GenerateRequest) (*manifest.Manifest, error) {
	settings := req.GetSettings()
	if e := settings.GetEngine(); e != "postgresql" {
		return nil, fmt.Errorf("sqlc-dynamic supports only engine postgresql, got %q", e)
	}
	var opts manifest.Options
	if len(bytes.TrimSpace(req.GetPluginOptions())) > 0 {
		dec := json.NewDecoder(bytes.NewReader(req.GetPluginOptions()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&opts); err != nil {
			return nil, fmt.Errorf("sqlc-dynamic: invalid plugin options: %w", err)
		}
	}
	cat := req.GetCatalog()
	m := &manifest.Manifest{
		Version:       manifest.Version,
		SQLCVersion:   req.GetSqlcVersion(),
		Engine:        settings.GetEngine(),
		DefaultSchema: cat.GetDefaultSchema(),
		Options:       opts,
	}
	for _, s := range cat.GetSchemas() {
		if s.GetName() == "pg_catalog" || s.GetName() == "information_schema" {
			continue
		}
		ms := manifest.Schema{Name: s.GetName()}
		for _, t := range s.GetTables() {
			mt := manifest.Table{
				Schema:  t.GetRel().GetSchema(),
				Name:    t.GetRel().GetName(),
				Comment: t.GetComment(),
			}
			if mt.Schema == "" {
				mt.Schema = s.GetName()
			}
			for _, c := range t.GetColumns() {
				mt.Columns = append(mt.Columns, column(c))
			}
			ms.Tables = append(ms.Tables, mt)
		}
		for _, e := range s.GetEnums() {
			ms.Enums = append(ms.Enums, manifest.Enum{Name: e.GetName(), Values: append([]string{}, e.GetVals()...)})
		}
		m.Schemas = append(m.Schemas, ms)
	}
	for _, q := range req.GetQueries() {
		mq := manifest.Query{
			Name:     q.GetName(),
			Cmd:      q.GetCmd(),
			Text:     q.GetText(),
			Filename: q.GetFilename(),
		}
		for _, c := range q.GetComments() {
			mq.Comments = append(mq.Comments, strings.TrimSpace(c))
		}
		for _, c := range q.GetColumns() {
			mq.Columns = append(mq.Columns, column(c))
		}
		for _, p := range q.GetParams() {
			mq.Params = append(mq.Params, manifest.Param{Number: int(p.GetNumber()), Column: column(p.GetColumn())})
		}
		m.Queries = append(m.Queries, mq)
	}
	return m, nil
}

func ident(id *pb.Identifier) *manifest.Identifier {
	if id == nil || id.GetName() == "" {
		return nil
	}
	return &manifest.Identifier{Schema: id.GetSchema(), Name: id.GetName()}
}

func column(c *pb.Column) manifest.Column {
	if c == nil {
		return manifest.Column{}
	}
	mc := manifest.Column{
		Name:         c.GetName(),
		OriginalName: c.GetOriginalName(),
		NotNull:      c.GetNotNull(),
		IsArray:      c.GetIsArray(),
		ArrayDims:    int(c.GetArrayDims()),
		Comment:      c.GetComment(),
		Table:        ident(c.GetTable()),
		EmbedTable:   ident(c.GetEmbedTable()),
		IsSqlcSlice:  c.GetIsSqlcSlice(),
		IsNamedParam: c.GetIsNamedParam(),
		IsFuncCall:   c.GetIsFuncCall(),
	}
	if t := c.GetType(); t != nil {
		mc.Type = manifest.Identifier{Schema: t.GetSchema(), Name: t.GetName()}
	}
	return mc
}
