package webapp

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Row is one piece of data received from ThaID, ready for display.
type Row struct {
	Key, Label, Value string
	Sources           []string
}

// Labels for claims ThaID can return. Unknown claims are shown by key.
var claimLabels = map[string]string{
	"pid":              "เลขประจำตัวประชาชน",
	"title":            "คำนำหน้า",
	"titleTh":          "คำนำหน้า (ไทย)",
	"version":          "เวอร์ชัน Token",
	"at_hash":          "ค่าตรวจสอบ Access Token (at_hash)",
	"given_name":       "ชื่อ",
	"middle_name":      "ชื่อกลาง",
	"family_name":      "นามสกุล",
	"name":             "ชื่อ-นามสกุล",
	"title_en":         "คำนำหน้า (อังกฤษ)",
	"given_name_en":    "ชื่อ (อังกฤษ)",
	"middle_name_en":   "ชื่อกลาง (อังกฤษ)",
	"family_name_en":   "นามสกุล (อังกฤษ)",
	"name_en":          "ชื่อ-นามสกุล (อังกฤษ)",
	"gender":           "เพศ",
	"birthdate":        "วันเกิด",
	"address":          "ที่อยู่",
	"house_address":    "ที่อยู่ตามทะเบียนบ้าน",
	"smartcard_code":   "รหัสหลังบัตร (Laser code)",
	"date_of_issuance": "วันออกบัตร",
	"date_of_expiry":   "วันบัตรหมดอายุ",
	"ial":              "ระดับความน่าเชื่อถือตัวตน (IAL)",
	"sub":              "รหัสผู้ใช้ใน ThaID (sub)",
	"iss":              "ผู้ออก Token (iss)",
	"aud":              "ออกให้ระบบ (aud)",
	"iat":              "ออก Token เมื่อ (iat)",
	"exp":              "Token หมดอายุ (exp)",
	"auth_time":        "ยืนยันตัวตนเมื่อ (auth_time)",
	"nonce":            "nonce",
	"scope":            "Scope ที่ได้รับ",
	"token_type":       "ประเภท Token",
	"expires_in":       "อายุ Access Token (วินาที)",
}

// Display order for personal data; anything else follows alphabetically.
var personOrder = []string{
	"pid", "title", "titleTh", "given_name", "middle_name", "family_name", "name",
	"title_en", "given_name_en", "middle_name_en", "family_name_en", "name_en",
	"gender", "birthdate", "address", "house_address", "smartcard_code",
	"date_of_issuance", "date_of_expiry", "ial",
}

// Claims about the login itself rather than about the person.
var technicalKeys = []string{"version", "sub", "iss", "aud", "iat", "exp", "auth_time", "nonce", "scope", "token_type", "expires_in", "at_hash", "azp", "jti", "sid", "amr", "acr"}

var timeKeys = []string{"iat", "exp", "auth_time", "nbf"}

type source struct {
	name string
	data map[string]any
}

// buildRows merges the data from all sources, keeping track of where each
// value came from, and splits it into personal and technical rows.
func buildRows(sources ...source) (person, technical []Row) {
	byKey := map[string]*Row{}
	var keys []string
	for _, src := range sources {
		for k, v := range src.data {
			val := formatValue(k, v)
			if r, ok := byKey[k]; ok {
				if r.Value == val {
					r.Sources = append(r.Sources, src.name)
				} else {
					r.Value += " / " + val + " (" + src.name + ")"
				}
				continue
			}
			label := claimLabels[k]
			if label == "" {
				label = k
			}
			byKey[k] = &Row{Key: k, Label: label, Value: val, Sources: []string{src.name}}
			keys = append(keys, k)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := slices.Index(personOrder, keys[i]), slices.Index(personOrder, keys[j])
		if a < 0 {
			a = len(personOrder)
		}
		if b < 0 {
			b = len(personOrder)
		}
		if a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		if slices.Contains(technicalKeys, k) {
			technical = append(technical, *byKey[k])
		} else {
			person = append(person, *byKey[k])
		}
	}
	return person, technical
}

func formatValue(key string, v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case string:
		if key == "pid" {
			return FormatPID(x)
		}
		if x == "" {
			return "-"
		}
		return x
	case float64:
		if slices.Contains(timeKeys, key) {
			return time.Unix(int64(x), 0).Format("02/01/2006 15:04:05")
		}
		if x == math.Trunc(x) {
			return fmt.Sprintf("%.0f", x)
		}
		return fmt.Sprint(x)
	case bool:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = formatValue("", p)
		}
		return strings.Join(parts, ", ")
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// FormatPID writes a 13-digit citizen ID as x-xxxx-xxxxx-xx-x.
func FormatPID(pid string) string {
	if len(pid) != 13 {
		return pid
	}
	return pid[0:1] + "-" + pid[1:5] + "-" + pid[5:10] + "-" + pid[10:12] + "-" + pid[12:]
}
