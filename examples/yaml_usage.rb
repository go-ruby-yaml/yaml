# frozen_string_literal: true

require "yaml"

# YAML.dump serialises a tree of Ruby values to a Psych-compatible document.
config = { "name" => "web-server", "port" => 8080, "enabled" => true, "tags" => ["prod", "eu"] }
document = YAML.dump(config)
puts document

# YAML.load parses a document string back into Ruby values (round-trip).
loaded = YAML.load(document)
p loaded
puts "port: #{loaded["port"]}"

# Object#to_yaml is a shorthand for YAML.dump(self).
puts({ "a" => 1, "b" => [2, 3] }.to_yaml)

# YAML.safe_load restricts which classes may materialise; scalars/arrays/hashes are always allowed.
p YAML.safe_load("- 1\n- 2\n- 3\n")

puts "Psych VERSION: #{YAML::VERSION}"
