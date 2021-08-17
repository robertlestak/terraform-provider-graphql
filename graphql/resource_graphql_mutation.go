package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceGraphqlMutation() *schema.Resource {
	return &schema.Resource{
		Schema: map[string]*schema.Schema{
			"read_query": {
				Type:     schema.TypeString,
				Required: true,
			},
			"create_mutation": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"delete_mutation": {
				Type:     schema.TypeString,
				Required: true,
			},
			"update_mutation": {
				Type:     schema.TypeString,
				Required: true,
			},
			"mutation_variables": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Required: true,
			},
			"read_query_variables": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Optional: true,
			},
			"delete_mutation_variables": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Optional: true,
			},
			"compute_mutation_keys": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Required: true,
			},
			"compute_from_create": {
				Type:     schema.TypeBool,
				Optional: true,
			},
			"computed_read_operation_variables": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Computed: true,
			},
			"computed_update_operation_variables": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Computed: true,
			},
			"computed_delete_operation_variables": {
				Type: schema.TypeMap,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Computed: true,
			},
			"query_response": {
				Type:        schema.TypeString,
				Description: "The raw body of the HTTP response from the last read of the object.",
				Computed:    true,
			},
			"existing_hash": {
				Type:        schema.TypeString,
				Description: "Represents the state of existence of a mutation in order to support intelligent updates.",
				Computed:    true,
			},
		},
		CreateContext: resourceGraphqlMutationCreateUpdate,
		UpdateContext: resourceGraphqlMutationCreateUpdate,
		ReadContext:   resourceGraphqlRead,
		DeleteContext: resourceGraphqlMutationDelete,
	}
}

func resourceGraphqlMutationCreateUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	mutationVariables := d.Get("mutation_variables").(map[string]interface{})
	var resBytes []byte
	var queryResponse *GqlQueryResponse
	var err error
	mutationExistsHash := d.Get("existing_hash").(string)

	if mutationExistsHash == "" {
		queryResponse, resBytes, err = queryExecute(ctx, d, m, "create_mutation", "mutation_variables")
		if err != nil {
			return diag.FromErr(err)
		}

		if queryErrors := queryResponse.ProcessErrors(); queryErrors.HasError() {
			return *queryErrors
		}

		existingHash := hash(resBytes)
		if err := d.Set("existing_hash", fmt.Sprint(existingHash)); err != nil {
			return diag.FromErr(err)
		}

		computeFromCreate := d.Get("compute_from_create").(bool)
		if computeFromCreate {
			if err := computeMutationVariables(resBytes, d); err != nil {
				return diag.FromErr(err)
			}
		}

	} else {
		computedVariables := d.Get("computed_update_operation_variables").(map[string]interface{})

		for k, v := range mutationVariables {
			computedVariables[k] = v
		}

		if err := d.Set("computed_update_operation_variables", computedVariables); err != nil {
			return diag.FromErr(err)
		}

		queryResponse, resBytes, err = queryExecute(ctx, d, m, "update_mutation", "computed_update_operation_variables")
		if err != nil {
			return diag.FromErr(err)
		}

		if queryErrors := queryResponse.ProcessErrors(); queryErrors.HasError() {
			return *queryErrors
		}
	}
	objID := hash(resBytes)
	d.SetId(fmt.Sprint(objID))

	return resourceGraphqlRead(ctx, d, m)
}

func resourceGraphqlRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {

	queryVariables := d.Get("read_query_variables").(map[string]interface{})
	computedVariables := d.Get("computed_read_operation_variables").(map[string]interface{})

	for k, v := range queryVariables {
		computedVariables[k] = v
	}

	if err := d.Set("computed_read_operation_variables", computedVariables); err != nil {
		return diag.FromErr(err)
	}

	queryResponse, resBytes, err := queryExecute(ctx, d, m, "read_query", "computed_read_operation_variables")
	if err != nil {
		return diag.FromErr(err)
	}

	if queryErrors := queryResponse.ProcessErrors(); queryErrors.HasError() {
		return *queryErrors
	}

	if err := d.Set("query_response", string(resBytes)); err != nil {
		return diag.FromErr(err)
	}

	computeFromCreate := d.Get("compute_from_create").(bool)
	if !computeFromCreate {
		if err := computeMutationVariables(resBytes, d); err != nil {
			return diag.FromErr(err)
		}
	}

	return diag.Diagnostics{}
}

func resourceGraphqlMutationDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	queryResponse, _, err := queryExecute(ctx, d, m, "delete_mutation", "computed_delete_operation_variables")
	if err != nil {
		return diag.FromErr(err)
	}

	if queryErrors := queryResponse.ProcessErrors(); queryErrors.HasError() {
		return *queryErrors
	}

	return nil
}

func computeMutationVariables(queryResponseBytes []byte, d *schema.ResourceData) error {
	dataKeys := d.Get("compute_mutation_keys").(map[string]interface{})
	mutationVariables := d.Get("mutation_variables").(map[string]interface{})
	readQueryVariables := d.Get("read_query_variables").(map[string]interface{})
	deleteMutationVariables := d.Get("delete_mutation_variables").(map[string]interface{})

	var robj = make(map[string]interface{})
	err := json.Unmarshal(queryResponseBytes, &robj)
	if err != nil {
		return err
	}

	// Set delete mutation variables
	mvks, err := computeMutationVariableKeys(dataKeys, robj)
	if err != nil {
		log.Printf("[ERROR] Unable to compute mutation variable keys: %s ", err)
	} else {
		// Combine computed read mutation variables with provided input variables
		rvks := make(map[string]string)
		for k, v := range readQueryVariables {
			// rvks[k] = v.(string)
			if _, ok := v.(string); ok {
				rvks[k] = v.(string)
			} else {
				bytes, err := json.Marshal(v)
				if err != nil {
					return err
				}
				rvks[k] = string(bytes)
			}
		}
		for k, v := range mvks {
			rvks[k] = v
		}
		if err := d.Set("computed_read_operation_variables", rvks); err != nil {
			return err
		}

		// Combine computed delete mutation variables with provided input variables
		dvks := make(map[string]string)
		for k, v := range deleteMutationVariables {
			if _, ok := v.(string); ok {
				dvks[k] = v.(string)
			} else {
				bytes, err := json.Marshal(v)
				if err != nil {
					return err
				}
				dvks[k] = string(bytes)
			}
		}
		for k, v := range mvks {
			dvks[k] = v
		}
		if err := d.Set("computed_delete_operation_variables", dvks); err != nil {
			return err
		}

		// Combine computed update mutation variables with provided input variables
		for k, v := range mutationVariables {
			if _, ok := v.(string); ok {
				mvks[k] = v.(string)
			} else {
				bytes, err := json.Marshal(v)
				if err != nil {
					return err
				}
				mvks[k] = string(bytes)
			}
		}
		if err := d.Set("computed_update_operation_variables", mvks); err != nil {
			return err
		}
	}

	return nil
}
