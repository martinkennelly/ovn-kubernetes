// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package sbdb

// PortGroup defines an object in Port_Group table
type PortGroup struct {
	UUID  string   `ovsdb:"_uuid"`
	Name  string   `ovsdb:"name"`
	Ports []string `ovsdb:"ports"`
}