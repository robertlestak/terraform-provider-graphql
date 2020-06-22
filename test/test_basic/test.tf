provider "graphql" {
  url = "http://localhost:8080/query"
  headers = {
    "x-api-key": "5555443399"
  }
}

data "graphql_query" "basic_query" {
  depends_on = [graphql_mutation.basic_mutation]
  read_query_variables = {}
  read_query     = file("./queries/readQuery")
}

resource "graphql_mutation" "basic_mutation" {
  create_mutation_variables = {
    "text" = "Here is something todo"
    "userId" = "98"
  }
  # if update, create, and read variables are omitted, they will fall back to the required create_mutation_variables
  update_mutation_variables = {}
  read_query_variables = {}
  # Reference files instead of inline queries to keep tf files clean. See examplquery for an example of a query file
  create_mutation = file("./queries/createMutation")
  update_mutation = file("./queries/updateMutation")
  delete_mutation = file("./queries/deleteMutation")
  read_query      = file("./queries/readQuery")

  query_response_key_map = ["todo.id"]
}

output "myoutput" {
  value = graphql_mutation.basic_mutation
}

output "myitem" {
  value = data.graphql_query.basic_query
}