package resolvers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/models/schema"
	"github.com/pocketbase/pocketbase/tools/inflector"
	"github.com/pocketbase/pocketbase/tools/list"
	"github.com/pocketbase/pocketbase/tools/search"
	"github.com/pocketbase/pocketbase/tools/security"
	"github.com/spf13/cast"
)

const (
	selectEachModifier = "each"
	issetModifier      = "isset"
)

// ensure that `search.FieldResolver` interface is implemented
var _ search.FieldResolver = (*RecordFieldResolver)(nil)

// list of auth filter fields that don't require join with the auth
// collection or any other extra checks to be resolved
var plainRequestAuthFields = []string{
	"@request.auth." + schema.FieldNameId,
	"@request.auth." + schema.FieldNameCollectionId,
	"@request.auth." + schema.FieldNameCollectionName,
	"@request.auth." + schema.FieldNameUsername,
	"@request.auth." + schema.FieldNameEmail,
	"@request.auth." + schema.FieldNameEmailVisibility,
	"@request.auth." + schema.FieldNameVerified,
	"@request.auth." + schema.FieldNameCreated,
	"@request.auth." + schema.FieldNameUpdated,
}

// RecordFieldResolver defines a custom search resolver struct for
// managing Record model search fields.
//
// Usually used together with `search.Provider`. Example:
//	resolver := resolvers.NewRecordFieldResolver(
//		app.Dao(),
//		myCollection,
//		&models.RequestData{...},
//		true,
//	)
//	provider := search.NewProvider(resolver)
//	...
type RecordFieldResolver struct {
	dao               *daos.Dao
	baseCollection    *models.Collection
	allowHiddenFields bool
	allowedFields     []string
	loadedCollections []*models.Collection
	joins             []*join // we cannot use a map because the insertion order is not preserved
	requestData       *models.RequestData
	staticRequestData map[string]any
}

// NewRecordFieldResolver creates and initializes a new `RecordFieldResolver`.
func NewRecordFieldResolver(
	dao *daos.Dao,
	baseCollection *models.Collection,
	requestData *models.RequestData,
	allowHiddenFields bool,
) *RecordFieldResolver {
	r := &RecordFieldResolver{
		dao:               dao,
		baseCollection:    baseCollection,
		requestData:       requestData,
		allowHiddenFields: allowHiddenFields,
		joins:             []*join{},
		loadedCollections: []*models.Collection{baseCollection},
		allowedFields: []string{
			`^\w+[\w\.]*$`,
			`^\@request\.method$`,
			`^\@request\.auth\.\w+[\w\.]*$`,
			`^\@request\.data\.\w+[\w\.]*$`,
			`^\@request\.query\.\w+[\w\.]*$`,
			`^\@collection\.\w+\.\w+[\w\.]*$`,
		},
	}

	r.staticRequestData = map[string]any{}
	if r.requestData != nil {
		r.staticRequestData["method"] = r.requestData.Method
		r.staticRequestData["query"] = r.requestData.Query
		r.staticRequestData["data"] = r.requestData.Data
		r.staticRequestData["auth"] = nil
		if r.requestData.AuthRecord != nil {
			r.requestData.AuthRecord.IgnoreEmailVisibility(true)
			r.staticRequestData["auth"] = r.requestData.AuthRecord.PublicExport()
			r.requestData.AuthRecord.IgnoreEmailVisibility(false)
		}
	}

	return r
}

// UpdateQuery implements `search.FieldResolver` interface.
//
// Conditionally updates the provided search query based on the
// resolved fields (eg. dynamically joining relations).
func (r *RecordFieldResolver) UpdateQuery(query *dbx.SelectQuery) error {
	if len(r.joins) > 0 {
		query.Distinct(true)

		for _, join := range r.joins {
			query.LeftJoin(
				(join.tableName + " " + join.tableAlias),
				join.on,
			)
		}
	}

	return nil
}

// Resolve implements `search.FieldResolver` interface.
//
// Example of resolvable field formats:
//
//	id
//	someSelect.each
//	project.screen.status
//	@request.status
//	@request.query.filter
//	@request.auth.someRelation.name
//	@request.data.someRelation.name
//	@request.data.someField
//	@request.data.someSelect.each
//	@request.data.someField.isset
//	@collection.product.name
//
// @todo convert a single Resolve execution into a separate struct with smaller logical chunks
func (r *RecordFieldResolver) Resolve(fieldName string) (*search.ResolverResult, error) {
	if len(r.allowedFields) > 0 && !list.ExistInSliceWithRegex(fieldName, r.allowedFields) {
		return nil, fmt.Errorf("failed to resolve field %q", fieldName)
	}

	props := strings.Split(fieldName, ".")

	currentCollectionName := r.baseCollection.Name
	currentTableAlias := inflector.Columnify(currentCollectionName)

	allowHiddenFields := r.allowHiddenFields

	// flag indicating whether to return null on missing field or return on an error
	nullifyMisingField := false

	// prepare a multi-match subquery
	mm := &multiMatchSubquery{
		baseTableAlias: currentTableAlias,
		params:         dbx.Params{},
	}
	mm.fromTableName = inflector.Columnify(currentCollectionName)
	mm.fromTableAlias = "__mm_" + currentTableAlias
	multiMatchCurrentTableAlias := mm.fromTableAlias
	withMultiMatch := false

	// check for @collection field (aka. non-relational join)
	// must be in the format "@collection.COLLECTION_NAME.FIELD[.FIELD2....]"
	if props[0] == "@collection" {
		if len(props) < 3 {
			return nil, fmt.Errorf("invalid @collection field path in %q", fieldName)
		}

		collection, err := r.loadCollection(props[1])
		if err != nil {
			return nil, fmt.Errorf("failed to load collection %q from field path %q", props[1], fieldName)
		}

		currentCollectionName = collection.Name
		currentTableAlias = inflector.Columnify("__collection_" + currentCollectionName)

		withMultiMatch = true

		// always allow hidden fields since the @collection.* filter is a system one
		allowHiddenFields = true

		// join the collection to the main query
		r.registerJoin(inflector.Columnify(collection.Name), currentTableAlias, nil)

		// join the collection to the multi-match subquery
		multiMatchCurrentTableAlias = "__mm" + currentTableAlias
		mm.joins = append(mm.joins, &join{
			tableName:  inflector.Columnify(collection.Name),
			tableAlias: multiMatchCurrentTableAlias,
		})

		// leave only the collection fields
		// aka. @collection.someCollection.fieldA.fieldB -> fieldA.fieldB
		props = props[2:]
	} else if props[0] == "@request" {
		if len(props) == 1 {
			return nil, fmt.Errorf("invalid @request data field path in %q", fieldName)
		}

		if r.requestData == nil {
			return &search.ResolverResult{Identifier: "NULL"}, nil
		}

		// always allow hidden fields since the @request.* filter is a system one
		allowHiddenFields = true

		// enable the ignore flag for missing @request.* fields for backward
		// compatibility and consistency with all @request.* filter fields and types
		nullifyMisingField = true

		// check for data select and relation fields
		if strings.HasPrefix(fieldName, "@request.data.") && len(props) > 3 {
			dataRelField := r.baseCollection.Schema.GetFieldByName(props[2])

			// data select.each field
			if dataRelField != nil && dataRelField.Type == schema.FieldTypeSelect && props[3] == selectEachModifier && len(props) == 4 {
				dataItems := list.ToUniqueStringSlice(r.requestData.Data[props[2]])
				rawJson, err := json.Marshal(dataItems)
				if err != nil {
					return nil, fmt.Errorf("cannot marshalize the data select item for field %q", props[2])
				}

				placeholder := "dataSelect" + security.PseudorandomString(4)
				cleanFieldName := inflector.Columnify(props[2])
				jeTable := fmt.Sprintf("json_each({:%s})", placeholder)
				jeAlias := "__dataSelect_" + cleanFieldName + "_je"
				r.registerJoin(jeTable, jeAlias, nil)

				result := &search.ResolverResult{
					Identifier: fmt.Sprintf("[[%s.value]]", jeAlias),
					Params:     dbx.Params{placeholder: rawJson},
				}

				dataRelField.InitOptions()
				options, ok := dataRelField.Options.(*schema.SelectOptions)
				if !ok {
					return nil, fmt.Errorf("failed to initialize field %q options", props[2])
				}

				if options.MaxSelect != 1 {
					withMultiMatch = true
				}

				if withMultiMatch {
					placeholder2 := "mm" + placeholder
					jeTable2 := fmt.Sprintf("json_each({:%s})", placeholder2)
					jeAlias2 := "__mm" + jeAlias

					mm.joins = append(mm.joins, &join{
						tableName:  jeTable2,
						tableAlias: jeAlias2,
					})
					mm.params[placeholder2] = rawJson
					mm.valueIdentifier = fmt.Sprintf("[[%s.value]]", jeAlias2)

					result.MultiMatchSubQuery = mm
				}

				return result, nil
			}

			// fallback to the static resolver for empty and non-relational data fields
			if dataRelField == nil || dataRelField.Type != schema.FieldTypeRelation {
				return r.resolveStaticRequestField(props[1:]...)
			}

			dataRelField.InitOptions()
			dataRelFieldOptions, ok := dataRelField.Options.(*schema.RelationOptions)
			if !ok {
				return nil, fmt.Errorf("failed to initialize data field %q options", dataRelField.Name)
			}

			dataRelCollection, err := r.loadCollection(dataRelFieldOptions.CollectionId)
			if err != nil {
				return nil, fmt.Errorf("failed to load collection %q from data field %q", dataRelFieldOptions.CollectionId, dataRelField.Name)
			}

			var dataRelIds []string
			if len(r.requestData.Data) != 0 {
				dataRelIds = list.ToUniqueStringSlice(r.requestData.Data[dataRelField.Name])
			}
			if len(dataRelIds) == 0 {
				return &search.ResolverResult{Identifier: "NULL"}, nil
			}

			currentCollectionName = dataRelCollection.Name
			currentTableAlias = inflector.Columnify("__data_" + dataRelCollection.Name)

			// join the data rel collection to the main collection
			r.registerJoin(
				inflector.Columnify(currentCollectionName),
				currentTableAlias,
				dbx.In(
					fmt.Sprintf("[[%s.id]]", inflector.Columnify(currentTableAlias)),
					list.ToInterfaceSlice(dataRelIds)...,
				),
			)

			if dataRelFieldOptions.MaxSelect == nil || *dataRelFieldOptions.MaxSelect != 1 {
				withMultiMatch = true
			}

			// join the data rel collection to the multi-match subquery
			multiMatchCurrentTableAlias = inflector.Columnify("__data_mm_" + dataRelCollection.Name)
			mm.joins = append(
				mm.joins,
				&join{
					tableName:  inflector.Columnify(currentCollectionName),
					tableAlias: multiMatchCurrentTableAlias,
					on:         dbx.In(multiMatchCurrentTableAlias+".id", list.ToInterfaceSlice(dataRelIds)...),
				},
			)

			// leave only the data relation fields
			// aka. @request.data.someRel.fieldA.fieldB -> fieldA.fieldB
			props = props[3:]
		} else {
			// plain @request.* field
			if !strings.HasPrefix(fieldName, "@request.auth.") || list.ExistInSlice(fieldName, plainRequestAuthFields) {
				return r.resolveStaticRequestField(props[1:]...)
			}

			// resolve the auth collection fields
			// ---
			if r.requestData == nil || r.requestData.AuthRecord == nil || r.requestData.AuthRecord.Collection() == nil {
				return &search.ResolverResult{Identifier: "NULL"}, nil
			}

			collection := r.requestData.AuthRecord.Collection()
			r.loadedCollections = append(r.loadedCollections, collection)

			currentCollectionName = collection.Name
			currentTableAlias = "__auth_" + inflector.Columnify(currentCollectionName)

			// join the auth collection to the main query
			r.registerJoin(
				inflector.Columnify(currentCollectionName),
				currentTableAlias,
				dbx.HashExp{
					// aka. __auth_users.id = :userId
					(currentTableAlias + ".id"): r.requestData.AuthRecord.Id,
				},
			)

			// join the auth collection to the multi-match subquery
			multiMatchCurrentTableAlias = "__mm_" + currentTableAlias
			mm.joins = append(
				mm.joins,
				&join{
					tableName:  inflector.Columnify(currentCollectionName),
					tableAlias: multiMatchCurrentTableAlias,
					on: dbx.HashExp{
						(multiMatchCurrentTableAlias + ".id"): r.requestData.AuthRecord.Id,
					},
				},
			)

			// leave only the auth relation fields
			// aka. @request.auth.fieldA.fieldB -> fieldA.fieldB
			props = props[2:]
		}
	}

	totalProps := len(props)

	for i, prop := range props {
		collection, err := r.loadCollection(currentCollectionName)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve field %q", prop)
		}

		systemFieldNames := schema.BaseModelFieldNames()
		if collection.IsAuth() {
			systemFieldNames = append(
				systemFieldNames,
				schema.FieldNameUsername,
				schema.FieldNameVerified,
				schema.FieldNameEmailVisibility,
				schema.FieldNameEmail,
			)
		}

		// internal model prop (always available but not part of the collection schema)
		if i == totalProps-1 && list.ExistInSlice(prop, systemFieldNames) {
			result := &search.ResolverResult{
				Identifier: fmt.Sprintf("[[%s.%s]]", currentTableAlias, inflector.Columnify(prop)),
			}

			// allow querying only auth records with emails marked as public
			if prop == schema.FieldNameEmail && !allowHiddenFields {
				result.AfterBuild = func(expr dbx.Expression) dbx.Expression {
					return dbx.And(expr, dbx.NewExp(fmt.Sprintf(
						"[[%s.%s]] = TRUE",
						currentTableAlias,
						schema.FieldNameEmailVisibility,
					)))
				}
			}

			if withMultiMatch {
				mm.valueIdentifier = fmt.Sprintf("[[%s.%s]]", multiMatchCurrentTableAlias, inflector.Columnify(prop))
				result.MultiMatchSubQuery = mm
			}

			return result, nil
		}

		field := collection.Schema.GetFieldByName(prop)
		if field == nil {
			if nullifyMisingField {
				return &search.ResolverResult{
					Identifier: "NULL",
				}, nil
			}

			return nil, fmt.Errorf("unrecognized field %q", prop)
		}

		// last prop
		if i == totalProps-1 {
			cleanFieldName := inflector.Columnify(prop)

			result := &search.ResolverResult{
				Identifier: fmt.Sprintf("[[%s.%s]]", currentTableAlias, cleanFieldName),
			}

			if withMultiMatch {
				mm.valueIdentifier = fmt.Sprintf("[[%s.%s]]", multiMatchCurrentTableAlias, cleanFieldName)
				result.MultiMatchSubQuery = mm
			}

			return result, nil
		}

		// check if it is a select ".each" field modifier
		if field.Type == schema.FieldTypeSelect && props[i+1] == selectEachModifier && i+2 == totalProps {
			cleanFieldName := inflector.Columnify(prop)
			jePair := currentTableAlias + "." + cleanFieldName
			jeAlias := currentTableAlias + "_" + cleanFieldName + "_je"
			r.registerJoin(jsonEach(jePair), jeAlias, nil)

			result := &search.ResolverResult{
				Identifier: fmt.Sprintf("[[%s.value]]", jeAlias),
			}

			field.InitOptions()
			options, ok := field.Options.(*schema.SelectOptions)
			if !ok {
				return nil, fmt.Errorf("failed to initialize field %q options", prop)
			}

			if options.MaxSelect != 1 {
				withMultiMatch = true
			}

			if withMultiMatch {
				jePair2 := multiMatchCurrentTableAlias + "." + cleanFieldName
				jeAlias2 := multiMatchCurrentTableAlias + "_" + cleanFieldName + "_je"

				mm.joins = append(
					mm.joins,
					&join{
						tableName:  jsonEach(jePair2),
						tableAlias: jeAlias2,
					},
				)

				mm.valueIdentifier = fmt.Sprintf("[[%s.value]]", jeAlias2)

				result.MultiMatchSubQuery = mm
			}

			return result, nil
		}

		// check if it is a json field
		if field.Type == schema.FieldTypeJson {
			var jsonPath strings.Builder
			jsonPath.WriteString("$")
			for _, p := range props[i+1:] {
				if _, err := strconv.Atoi(p); err == nil {
					jsonPath.WriteString("[")
					jsonPath.WriteString(inflector.Columnify(p))
					jsonPath.WriteString("]")
				} else {
					jsonPath.WriteString(".")
					jsonPath.WriteString(inflector.Columnify(p))
				}
			}

			result := &search.ResolverResult{
				Identifier: fmt.Sprintf(
					"JSON_EXTRACT([[%s.%s]], '%s')",
					currentTableAlias,
					inflector.Columnify(prop),
					jsonPath.String(),
				),
			}

			if withMultiMatch {
				mm.valueIdentifier = fmt.Sprintf(
					"JSON_EXTRACT([[%s.%s]], '%s')",
					multiMatchCurrentTableAlias,
					inflector.Columnify(prop),
					jsonPath.String(),
				)
				result.MultiMatchSubQuery = mm
			}

			return result, nil
		}

		// check if it is a relation field
		if field.Type != schema.FieldTypeRelation {
			return nil, fmt.Errorf("field %q is not a valid relation", prop)
		}

		// join the relation to the main query
		// ---
		field.InitOptions()
		options, ok := field.Options.(*schema.RelationOptions)
		if !ok {
			return nil, fmt.Errorf("failed to initialize field %q options", prop)
		}

		relCollection, relErr := r.loadCollection(options.CollectionId)
		if relErr != nil {
			return nil, fmt.Errorf("failed to find field %q collection", prop)
		}

		cleanFieldName := inflector.Columnify(field.Name)
		newCollectionName := relCollection.Name
		newTableAlias := currentTableAlias + "_" + cleanFieldName

		jeAlias := currentTableAlias + "_" + cleanFieldName + "_je"
		jePair := currentTableAlias + "." + cleanFieldName
		r.registerJoin(jsonEach(jePair), jeAlias, nil)
		r.registerJoin(
			inflector.Columnify(newCollectionName),
			newTableAlias,
			dbx.NewExp(fmt.Sprintf("[[%s.id]] = [[%s.value]]", newTableAlias, jeAlias)),
		)
		currentCollectionName = newCollectionName
		currentTableAlias = newTableAlias
		// ---

		// join the relation to the multi-match subquery
		// ---
		if options.MaxSelect == nil || *options.MaxSelect != 1 {
			withMultiMatch = true
		}

		newTableAlias2 := multiMatchCurrentTableAlias + "_" + cleanFieldName
		jeAlias2 := multiMatchCurrentTableAlias + "_" + cleanFieldName + "_je"
		jePair2 := multiMatchCurrentTableAlias + "." + cleanFieldName
		multiMatchCurrentTableAlias = newTableAlias2

		mm.joins = append(
			mm.joins,
			&join{
				tableName:  jsonEach(jePair2),
				tableAlias: jeAlias2,
			},
			&join{
				tableName:  inflector.Columnify(newCollectionName),
				tableAlias: newTableAlias2,
				on:         dbx.NewExp(fmt.Sprintf("[[%s.id]] = [[%s.value]]", newTableAlias2, jeAlias2)),
			},
		)
		// ---
	}

	return nil, fmt.Errorf("failed to resolve field %q", fieldName)
}

func (r *RecordFieldResolver) resolveStaticRequestField(path ...string) (*search.ResolverResult, error) {
	hasIssetSuffix := len(path) > 0 && path[len(path)-1] == issetModifier
	if hasIssetSuffix {
		path = path[:len(path)-1]
	}

	resultVal, err := extractNestedMapVal(r.staticRequestData, path...)

	if hasIssetSuffix {
		if err != nil {
			return &search.ResolverResult{Identifier: "FALSE"}, nil
		}
		return &search.ResolverResult{Identifier: "TRUE"}, nil
	}

	// note: we are ignoring the error because requestData is dynamic
	// and some of the lookup keys may not be defined for the request

	switch v := resultVal.(type) {
	case nil:
		return &search.ResolverResult{Identifier: "NULL"}, nil
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		// no further processing is needed...
	default:
		// non-plain value
		// try casting to string (in case for exampe fmt.Stringer is implemented)
		val, castErr := cast.ToStringE(v)

		// if that doesn't work, try encoding it
		if castErr != nil {
			encoded, jsonErr := json.Marshal(v)
			if jsonErr == nil {
				val = string(encoded)
			}
		}

		resultVal = val
	}

	placeholder := "f" + security.PseudorandomString(5)

	return &search.ResolverResult{
		Identifier: "{:" + placeholder + "}",
		Params:     dbx.Params{placeholder: resultVal},
	}, nil
}

func (r *RecordFieldResolver) loadCollection(collectionNameOrId string) (*models.Collection, error) {
	// return already loaded
	for _, collection := range r.loadedCollections {
		if collection.Id == collectionNameOrId || strings.EqualFold(collection.Name, collectionNameOrId) {
			return collection, nil
		}
	}

	// load collection
	collection, err := r.dao.FindCollectionByNameOrId(collectionNameOrId)
	if err != nil {
		return nil, err
	}
	r.loadedCollections = append(r.loadedCollections, collection)

	return collection, nil
}

func (r *RecordFieldResolver) registerJoin(tableName string, tableAlias string, on dbx.Expression) {
	join := &join{
		tableName:  tableName,
		tableAlias: tableAlias,
		on:         on,
	}

	// replace existing join
	for i, j := range r.joins {
		if j.tableAlias == join.tableAlias {
			r.joins[i] = join
			return
		}
	}

	// register new join
	r.joins = append(r.joins, join)
}

func jsonEach(tableColumnPair string) string {
	return fmt.Sprintf(
		// note: the case is used to normalize value access for single and multiple relations.
		`json_each(CASE WHEN json_valid([[%s]]) THEN [[%s]] ELSE json_array([[%s]]) END)`,
		tableColumnPair, tableColumnPair, tableColumnPair,
	)
}

func extractNestedMapVal(m map[string]any, keys ...string) (result any, err error) {
	var ok bool

	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one key should be provided")
	}

	if result, ok = m[keys[0]]; !ok {
		return nil, fmt.Errorf("invalid key path - missing key %q", keys[0])
	}

	// end key reached
	if len(keys) == 1 {
		return result, nil
	}

	if m, ok = result.(map[string]any); !ok {
		return nil, fmt.Errorf("expected map, got %#v", result)
	}

	return extractNestedMapVal(m, keys[1:]...)
}
