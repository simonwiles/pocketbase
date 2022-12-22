package resolvers_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/resolvers"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/list"
	"github.com/pocketbase/pocketbase/tools/search"
)

func TestRecordFieldResolverUpdateQuery(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	authRecord, err := app.Dao().FindRecordById("users", "4q1xlclmfloku33")
	if err != nil {
		t.Fatal(err)
	}

	requestData := &models.RequestData{
		Query: map[string]any{
			"a": nil,
			"b": 123,
		},
		Data: map[string]any{
			"a":        nil,
			"b":        123,
			"rel_one":  "test",
			"rel_many": []string{"test1", "test2"},
		},
		AuthRecord: authRecord,
	}

	scenarios := []struct {
		name               string
		collectionIdOrName string
		fields             []string
		allowHiddenFields  bool
		expectQuery        string
	}{
		{
			"non relation field",
			"demo4",
			[]string{"title"},
			false,
			"SELECT `demo4`.* FROM `demo4` WHERE [[demo4.title]] > 1",
		},
		{
			"incomplete rel",
			"demo4",
			[]string{"self_rel_one"},
			false,
			"SELECT `demo4`.* FROM `demo4` WHERE [[demo4.self_rel_one]] > 1",
		},
		{
			"single rel (self rel)",
			"demo4",
			[]string{"self_rel_one.title"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_one]]) THEN [[demo4.self_rel_one]] ELSE json_array([[demo4.self_rel_one]]) END) `demo4_self_rel_one_je` LEFT JOIN `demo4` `demo4_self_rel_one` ON [[demo4_self_rel_one.id]] = [[demo4_self_rel_one_je.value]] WHERE [[demo4_self_rel_one.title]] > 1",
		},
		{
			"single rel (other collection)",
			"demo4",
			[]string{"rel_one_cascade.title"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.rel_one_cascade]]) THEN [[demo4.rel_one_cascade]] ELSE json_array([[demo4.rel_one_cascade]]) END) `demo4_rel_one_cascade_je` LEFT JOIN `demo3` `demo4_rel_one_cascade` ON [[demo4_rel_one_cascade.id]] = [[demo4_rel_one_cascade_je.value]] WHERE [[demo4_rel_one_cascade.title]] > 1",
		},
		{
			"non-relation field + single rel",
			"demo4",
			[]string{"title", "self_rel_one.title"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_one]]) THEN [[demo4.self_rel_one]] ELSE json_array([[demo4.self_rel_one]]) END) `demo4_self_rel_one_je` LEFT JOIN `demo4` `demo4_self_rel_one` ON [[demo4_self_rel_one.id]] = [[demo4_self_rel_one_je.value]] WHERE ([[demo4.title]] > 1 OR [[demo4_self_rel_one.title]] > 1)",
		},
		{
			"nested incomplete rels",
			"demo4",
			[]string{"self_rel_many.self_rel_one"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_many]]) THEN [[demo4.self_rel_many]] ELSE json_array([[demo4.self_rel_many]]) END) `demo4_self_rel_many_je` LEFT JOIN `demo4` `demo4_self_rel_many` ON [[demo4_self_rel_many.id]] = [[demo4_self_rel_many_je.value]] WHERE [[demo4_self_rel_many.self_rel_one]] > 1",
		},
		{
			"nested complete rels",
			"demo4",
			[]string{"self_rel_many.self_rel_one.title"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_many]]) THEN [[demo4.self_rel_many]] ELSE json_array([[demo4.self_rel_many]]) END) `demo4_self_rel_many_je` LEFT JOIN `demo4` `demo4_self_rel_many` ON [[demo4_self_rel_many.id]] = [[demo4_self_rel_many_je.value]] LEFT JOIN json_each(CASE WHEN json_valid([[demo4_self_rel_many.self_rel_one]]) THEN [[demo4_self_rel_many.self_rel_one]] ELSE json_array([[demo4_self_rel_many.self_rel_one]]) END) `demo4_self_rel_many_self_rel_one_je` LEFT JOIN `demo4` `demo4_self_rel_many_self_rel_one` ON [[demo4_self_rel_many_self_rel_one.id]] = [[demo4_self_rel_many_self_rel_one_je.value]] WHERE [[demo4_self_rel_many_self_rel_one.title]] > 1",
		},
		{
			"repeated nested rels",
			"demo4",
			[]string{"self_rel_many.self_rel_one.self_rel_many.self_rel_one.title"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_many]]) THEN [[demo4.self_rel_many]] ELSE json_array([[demo4.self_rel_many]]) END) `demo4_self_rel_many_je` LEFT JOIN `demo4` `demo4_self_rel_many` ON [[demo4_self_rel_many.id]] = [[demo4_self_rel_many_je.value]] LEFT JOIN json_each(CASE WHEN json_valid([[demo4_self_rel_many.self_rel_one]]) THEN [[demo4_self_rel_many.self_rel_one]] ELSE json_array([[demo4_self_rel_many.self_rel_one]]) END) `demo4_self_rel_many_self_rel_one_je` LEFT JOIN `demo4` `demo4_self_rel_many_self_rel_one` ON [[demo4_self_rel_many_self_rel_one.id]] = [[demo4_self_rel_many_self_rel_one_je.value]] LEFT JOIN json_each(CASE WHEN json_valid([[demo4_self_rel_many_self_rel_one.self_rel_many]]) THEN [[demo4_self_rel_many_self_rel_one.self_rel_many]] ELSE json_array([[demo4_self_rel_many_self_rel_one.self_rel_many]]) END) `demo4_self_rel_many_self_rel_one_self_rel_many_je` LEFT JOIN `demo4` `demo4_self_rel_many_self_rel_one_self_rel_many` ON [[demo4_self_rel_many_self_rel_one_self_rel_many.id]] = [[demo4_self_rel_many_self_rel_one_self_rel_many_je.value]] LEFT JOIN json_each(CASE WHEN json_valid([[demo4_self_rel_many_self_rel_one_self_rel_many.self_rel_one]]) THEN [[demo4_self_rel_many_self_rel_one_self_rel_many.self_rel_one]] ELSE json_array([[demo4_self_rel_many_self_rel_one_self_rel_many.self_rel_one]]) END) `demo4_self_rel_many_self_rel_one_self_rel_many_self_rel_one_je` LEFT JOIN `demo4` `demo4_self_rel_many_self_rel_one_self_rel_many_self_rel_one` ON [[demo4_self_rel_many_self_rel_one_self_rel_many_self_rel_one.id]] = [[demo4_self_rel_many_self_rel_one_self_rel_many_self_rel_one_je.value]] WHERE [[demo4_self_rel_many_self_rel_one_self_rel_many_self_rel_one.title]] > 1",
		},
		{
			"multiple rels",
			"demo4",
			[]string{"self_rel_many.title", "self_rel_one.json_object.a"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_many]]) THEN [[demo4.self_rel_many]] ELSE json_array([[demo4.self_rel_many]]) END) `demo4_self_rel_many_je` LEFT JOIN `demo4` `demo4_self_rel_many` ON [[demo4_self_rel_many.id]] = [[demo4_self_rel_many_je.value]] LEFT JOIN json_each(CASE WHEN json_valid([[demo4.self_rel_one]]) THEN [[demo4.self_rel_one]] ELSE json_array([[demo4.self_rel_one]]) END) `demo4_self_rel_one_je` LEFT JOIN `demo4` `demo4_self_rel_one` ON [[demo4_self_rel_one.id]] = [[demo4_self_rel_one_je.value]] WHERE ([[demo4_self_rel_many.title]] > 1 OR JSON_EXTRACT([[demo4_self_rel_one.json_object]], '$.a') > 1)",
		},
		{
			"@collection join",
			"demo4",
			[]string{"@collection.demo1.text", "@collection.demo2.active", "@collection.demo1.file_one"},
			false,
			"SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN `demo1` `__collection_demo1` LEFT JOIN `demo2` `__collection_demo2` WHERE ([[__collection_demo1.text]] > 1 OR [[__collection_demo2.active]] > 1 OR [[__collection_demo1.file_one]] > 1)",
		},
		{
			"@request.auth fields",
			"demo4",
			[]string{"@request.auth.id", "@request.auth.username", "@request.auth.rel.title", "@request.data.demo"},
			false,
			"^" +
				regexp.QuoteMeta("SELECT DISTINCT `demo4`.* FROM `demo4`") +
				regexp.QuoteMeta(" LEFT JOIN `users` `__auth_users` ON [[__auth_users.id]] = {:") +
				".+" +
				regexp.QuoteMeta("} LEFT JOIN json_each(CASE WHEN json_valid([[__auth_users.rel]]) THEN [[__auth_users.rel]] ELSE json_array([[__auth_users.rel]]) END) `__auth_users_rel_je`") +
				regexp.QuoteMeta(" LEFT JOIN `demo2` `__auth_users_rel` ON [[__auth_users_rel.id]] = [[__auth_users_rel_je.value]]") +
				regexp.QuoteMeta(" WHERE ({:") +
				".+" +
				regexp.QuoteMeta("} > 1 OR {:") +
				".+" +
				regexp.QuoteMeta("} > 1 OR [[__auth_users_rel.title]] > 1 OR NULL > 1)") +
				"$",
		},
		{
			"hidden field with system filters (ignore emailVisibility)",
			"demo4",
			[]string{"@collection.users.email", "@request.auth.email"},
			false,
			"^" +
				regexp.QuoteMeta("SELECT DISTINCT `demo4`.* FROM `demo4` LEFT JOIN `users` `__collection_users` WHERE ([[__collection_users.email]] > 1 OR {:") +
				".+" +
				regexp.QuoteMeta("} > 1)") +
				"$",
		},
		{
			"hidden field (add emailVisibility)",
			"users",
			[]string{"email"},
			false,
			"SELECT `users`.* FROM `users` WHERE (([[users.email]] > 1) AND ([[users.emailVisibility]] = TRUE))",
		},
		{
			"hidden field (force ignore emailVisibility)",
			"users",
			[]string{"email"},
			true,
			"SELECT `users`.* FROM `users` WHERE [[users.email]] > 1",
		},
		{
			"isset key",
			"demo1",
			[]string{
				"@request.data.a.isset",
				"@request.data.b.isset",
				"@request.data.c.isset",
				"@request.query.a.isset",
				"@request.query.b.isset",
				"@request.query.c.isset",
			},
			false,
			"SELECT `demo1`.* FROM `demo1` WHERE (TRUE > 1 OR TRUE > 1 OR FALSE > 1 OR TRUE > 1 OR TRUE > 1 OR FALSE > 1)",
		},
		{
			"@request.data.rel.* fields",
			"demo1",
			[]string{
				"@request.data.rel_one",
				"@request.data.rel_one.text",
				"@request.data.rel_many",
				"@request.data.rel_many.email",
			},
			false,
			"^" +
				regexp.QuoteMeta("SELECT DISTINCT `demo1`.* FROM `demo1`") +
				regexp.QuoteMeta(" LEFT JOIN `demo1` `__data_demo1` ON [[__data_demo1.id]]={:p0}") +
				regexp.QuoteMeta(" LEFT JOIN `users` `__data_users` ON [[__data_users.id]] IN ({:p1}, {:p2})") +
				regexp.QuoteMeta(" WHERE ({:") +
				".+" +
				regexp.QuoteMeta("} > 1 OR [[__data_demo1.text]] > 1 OR {:") +
				".+" +
				regexp.QuoteMeta("} > 1 OR [[__data_users.email]] > 1)") +
				"$",
		},
	}

	for _, s := range scenarios {
		collection, err := app.Dao().FindCollectionByNameOrId(s.collectionIdOrName)
		if err != nil {
			t.Fatalf("[%s] Failed to load collection %s: %v", s.name, s.collectionIdOrName, err)
		}

		query := app.Dao().RecordQuery(collection)

		r := resolvers.NewRecordFieldResolver(app.Dao(), collection, requestData, s.allowHiddenFields)

		var dummyData string
		for _, f := range s.fields {
			if dummyData != "" {
				dummyData += "||"
			}
			dummyData += f + " > true"
		}

		expr, err := search.FilterData(dummyData).BuildExpr(r)
		if err != nil {
			t.Fatalf("[%s] BuildExpr failed with error %v", s.name, err)
		}

		if err := r.UpdateQuery(query); err != nil {
			t.Fatalf("[%s] UpdateQuery failed with error %v", s.name, err)
		}

		rawQuery := query.AndWhere(expr).Build().SQL()

		if !list.ExistInSliceWithRegex(rawQuery, []string{s.expectQuery}) {
			t.Fatalf("[%s] Expected query\n %v \ngot:\n %v", s.name, s.expectQuery, rawQuery)
		}
	}
}

func TestRecordFieldResolverResolveSchemaFields(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	collection, err := app.Dao().FindCollectionByNameOrId("demo4")
	if err != nil {
		t.Fatal(err)
	}

	authRecord, err := app.Dao().FindRecordById("users", "4q1xlclmfloku33")
	if err != nil {
		t.Fatal(err)
	}

	requestData := &models.RequestData{
		AuthRecord: authRecord,
	}

	r := resolvers.NewRecordFieldResolver(app.Dao(), collection, requestData, true)

	scenarios := []struct {
		fieldName   string
		expectError bool
		expectName  string
	}{
		{"", true, ""},
		{" ", true, ""},
		{"unknown", true, ""},
		{"invalid format", true, ""},
		{"id", false, "[[demo4.id]]"},
		{"created", false, "[[demo4.created]]"},
		{"updated", false, "[[demo4.updated]]"},
		{"title", false, "[[demo4.title]]"},
		{"title.test", true, ""},
		{"self_rel_many", false, "[[demo4.self_rel_many]]"},
		{"self_rel_many.", true, ""},
		{"self_rel_many.unknown", true, ""},
		{"self_rel_many.title", false, "[[demo4_self_rel_many.title]]"},
		{"self_rel_many.self_rel_one.self_rel_many.title", false, "[[demo4_self_rel_many_self_rel_one_self_rel_many.title]]"},
		// json_extract
		{"json_array.0", false, "JSON_EXTRACT([[demo4.json_array]], '$[0]')"},
		{"json_object.a.b.c", false, "JSON_EXTRACT([[demo4.json_object]], '$.a.b.c')"},
		// @request.auth relation join:
		{"@request.auth.rel", false, "[[__auth_users.rel]]"},
		{"@request.auth.rel.title", false, "[[__auth_users_rel.title]]"},
		// @collection fieds:
		{"@collect", true, ""},
		{"collection.demo4.title", true, ""},
		{"@collection", true, ""},
		{"@collection.unknown", true, ""},
		{"@collection.demo2", true, ""},
		{"@collection.demo2.", true, ""},
		{"@collection.demo2.title", false, "[[__collection_demo2.title]]"},
		{"@collection.demo4.title", false, "[[__collection_demo4.title]]"},
		{"@collection.demo4.id", false, "[[__collection_demo4.id]]"},
		{"@collection.demo4.created", false, "[[__collection_demo4.created]]"},
		{"@collection.demo4.updated", false, "[[__collection_demo4.updated]]"},
		{"@collection.demo4.self_rel_many.missing", true, ""},
		{"@collection.demo4.self_rel_many.self_rel_one.self_rel_many.self_rel_one.title", false, "[[__collection_demo4_self_rel_many_self_rel_one_self_rel_many_self_rel_one.title]]"},
	}

	for _, s := range scenarios {
		r, err := r.Resolve(s.fieldName)

		hasErr := err != nil
		if hasErr != s.expectError {
			t.Errorf("(%q) Expected hasErr %v, got %v (%v)", s.fieldName, s.expectError, hasErr, err)
			continue
		}

		if hasErr {
			continue
		}

		if r.Identifier != s.expectName {
			t.Errorf("(%q) Expected r.Identifier %q, got %q", s.fieldName, s.expectName, r.Identifier)
		}

		// params should be empty for non @request fields
		if len(r.Params) != 0 {
			t.Errorf("(%q) Expected 0 r.Params, got %v", s.fieldName, r.Params)
		}
	}
}

func TestRecordFieldResolverResolveStaticRequestDataFields(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	collection, err := app.Dao().FindCollectionByNameOrId("demo4")
	if err != nil {
		t.Fatal(err)
	}

	authRecord, err := app.Dao().FindRecordById("users", "4q1xlclmfloku33")
	if err != nil {
		t.Fatal(err)
	}

	requestData := &models.RequestData{
		Method: "get",
		Query: map[string]any{
			"a": 123,
		},
		Data: map[string]any{
			"b": 456,
			"c": map[string]int{"sub": 1},
		},
		AuthRecord: authRecord,
	}

	r := resolvers.NewRecordFieldResolver(app.Dao(), collection, requestData, true)

	scenarios := []struct {
		fieldName        string
		expectError      bool
		expectParamValue string // encoded json
	}{
		{"@request", true, ""},
		{"@request.invalid format", true, ""},
		{"@request.invalid_format2!", true, ""},
		{"@request.missing", true, ""},
		{"@request.method", false, `"get"`},
		{"@request.query", true, ``},
		{"@request.query.a", false, `123`},
		{"@request.query.a.missing", false, ``},
		{"@request.data", true, ``},
		{"@request.data.b", false, `456`},
		{"@request.data.b.missing", false, ``},
		{"@request.data.c", false, `"{\"sub\":1}"`},
		{"@request.auth", true, ""},
		{"@request.auth.id", false, `"4q1xlclmfloku33"`},
		{"@request.auth.email", false, `"test@example.com"`},
		{"@request.auth.username", false, `"users75657"`},
		{"@request.auth.verified", false, `false`},
		{"@request.auth.emailVisibility", false, `false`},
		{"@request.auth.missing", false, `NULL`},
	}

	for i, s := range scenarios {
		r, err := r.Resolve(s.fieldName)

		hasErr := err != nil
		if hasErr != s.expectError {
			t.Errorf("(%d) Expected hasErr %v, got %v (%v)", i, s.expectError, hasErr, err)
			continue
		}

		if hasErr {
			continue
		}

		// missing key
		// ---
		if len(r.Params) == 0 {
			if r.Identifier != "NULL" {
				t.Errorf("(%d) Expected 0 placeholder parameters for %v, got %v", i, r.Identifier, r.Params)
			}
			continue
		}

		// existing key
		// ---
		if len(r.Params) != 1 {
			t.Errorf("(%d) Expected 1 placeholder parameter for %v, got %v", i, r.Identifier, r.Params)
			continue
		}

		var paramName string
		var paramValue any
		for k, v := range r.Params {
			paramName = k
			paramValue = v
		}

		if r.Identifier != ("{:" + paramName + "}") {
			t.Errorf("(%d) Expected parameter r.Identifier %q, got %q", i, paramName, r.Identifier)
		}

		encodedParamValue, _ := json.Marshal(paramValue)
		if string(encodedParamValue) != s.expectParamValue {
			t.Errorf("(%d) Expected r.Params %v for %v, got %v", i, s.expectParamValue, r.Identifier, string(encodedParamValue))
		}
	}
}
