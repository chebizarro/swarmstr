package nip61

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const cashuHashToCurveDomain = "Secp256k1_HashToCurve_Cashu_"

type p2pkSecret struct {
	Nonce string
	Data  string
}

func parseP2PKSecret(serialized string) (*p2pkSecret, error) {
	var outer []json.RawMessage
	if err := json.Unmarshal([]byte(serialized), &outer); err != nil || len(outer) != 2 {
		return nil, fmt.Errorf("nip61: proof secret is not a NUT-10 P2PK secret")
	}
	var kind string
	if err := json.Unmarshal(outer[0], &kind); err != nil || kind != "P2PK" {
		return nil, fmt.Errorf("nip61: proof secret kind must be P2PK")
	}
	var payload struct {
		Nonce string            `json:"nonce"`
		Data  string            `json:"data"`
		Tags  []json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(outer[1], &payload); err != nil {
		return nil, fmt.Errorf("nip61: malformed P2PK secret: %w", err)
	}
	payload.Data = strings.ToLower(payload.Data)
	if strings.TrimSpace(payload.Nonce) == "" {
		return nil, fmt.Errorf("nip61: P2PK secret nonce is required")
	}
	if len(payload.Data) != 66 || (payload.Data[:2] != "02" && payload.Data[:2] != "03") {
		return nil, fmt.Errorf("nip61: P2PK secret data must be a compressed secp256k1 key")
	}
	dataBytes, err := hex.DecodeString(payload.Data)
	if err != nil {
		return nil, fmt.Errorf("nip61: invalid P2PK secret data: %w", err)
	}
	if _, err := secp256k1.ParsePubKey(dataBytes); err != nil {
		return nil, fmt.Errorf("nip61: invalid P2PK secret data: %w", err)
	}

	tags := make(map[string][]string, len(payload.Tags))
	for _, rawTag := range payload.Tags {
		var values []json.RawMessage
		if err := json.Unmarshal(rawTag, &values); err != nil || len(values) < 2 {
			return nil, fmt.Errorf("nip61: malformed NUT-11 tag")
		}
		var name string
		if err := json.Unmarshal(values[0], &name); err != nil || name == "" {
			return nil, fmt.Errorf("nip61: malformed NUT-11 tag name")
		}
		if _, duplicate := tags[name]; duplicate {
			return nil, fmt.Errorf("nip61: duplicate NUT-11 tag %q", name)
		}
		parsed := make([]string, 0, len(values)-1)
		for _, raw := range values[1:] {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				value = string(raw)
			}
			parsed = append(parsed, value)
		}
		tags[name] = parsed
	}
	if err := validateNUT11Tags(payload.Data, tags); err != nil {
		return nil, err
	}
	return &p2pkSecret{Nonce: payload.Nonce, Data: payload.Data}, nil
}

func validateNUT11Tags(primary string, tags map[string][]string) error {
	for name := range tags {
		switch name {
		case "sigflag", "pubkeys", "n_sigs", "locktime", "refund", "n_sigs_refund":
		default:
			return fmt.Errorf("nip61: unsupported NUT-11 tag %q", name)
		}
	}
	if values, ok := tags["sigflag"]; ok {
		if len(values) != 1 || (values[0] != "SIG_INPUTS" && values[0] != "SIG_ALL") {
			return fmt.Errorf("nip61: invalid NUT-11 sigflag")
		}
	}
	mainKeys := []string{primary}
	if values, ok := tags["pubkeys"]; ok {
		mainKeys = append(mainKeys, values...)
	}
	if err := validateUniqueCompressedKeys(mainKeys); err != nil {
		return err
	}
	refundKeys := tags["refund"]
	if len(refundKeys) > 0 {
		if err := validateUniqueCompressedKeys(refundKeys); err != nil {
			return err
		}
	}
	if values, ok := tags["n_sigs"]; ok {
		if err := validateThreshold(values, len(mainKeys), "n_sigs"); err != nil {
			return err
		}
	}
	if values, ok := tags["n_sigs_refund"]; ok {
		if err := validateThreshold(values, len(refundKeys), "n_sigs_refund"); err != nil {
			return err
		}
	}
	if values, ok := tags["locktime"]; ok {
		if len(values) != 1 {
			return fmt.Errorf("nip61: invalid NUT-11 locktime")
		}
		if _, err := strconv.ParseInt(values[0], 10, 64); err != nil {
			return fmt.Errorf("nip61: invalid NUT-11 locktime")
		}
	}
	return nil
}

func validateThreshold(values []string, keyCount int, name string) error {
	if len(values) != 1 {
		return fmt.Errorf("nip61: invalid NUT-11 %s", name)
	}
	threshold, err := strconv.Atoi(values[0])
	if err != nil || threshold <= 0 || threshold > keyCount {
		return fmt.Errorf("nip61: invalid NUT-11 %s", name)
	}
	return nil
}

func validateUniqueCompressedKeys(keys []string) error {
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.ToLower(key)
		decoded, err := hex.DecodeString(key)
		if err != nil || len(decoded) != 33 {
			return fmt.Errorf("nip61: invalid NUT-11 public key")
		}
		if _, err := secp256k1.ParsePubKey(decoded); err != nil {
			return fmt.Errorf("nip61: invalid NUT-11 public key: %w", err)
		}
		xOnly := key[2:]
		if _, duplicate := seen[xOnly]; duplicate {
			return fmt.Errorf("nip61: duplicate NUT-11 public key")
		}
		seen[xOnly] = struct{}{}
	}
	return nil
}

// VerifyProofDLEQ verifies a NUT-12 proof against a compressed mint amount key.
func VerifyProofDLEQ(proof Proof, mintAmountKeyHex string) error {
	keyBytes, err := hex.DecodeString(strings.TrimSpace(mintAmountKeyHex))
	if err != nil {
		return fmt.Errorf("invalid mint amount key: %w", err)
	}
	mintKey, err := secp256k1.ParsePubKey(keyBytes)
	if err != nil {
		return fmt.Errorf("invalid mint amount key: %w", err)
	}
	return verifyProofDLEQ(proof, mintKey)
}

func verifyProofDLEQ(proof Proof, mintKey *secp256k1.PublicKey) error {
	if proof.DLEQ == nil {
		return fmt.Errorf("DLEQ proof is required")
	}
	e, eBytes, err := parseScalar(proof.DLEQ.E)
	if err != nil {
		return fmt.Errorf("invalid e scalar: %w", err)
	}
	s, _, err := parseScalar(proof.DLEQ.S)
	if err != nil {
		return fmt.Errorf("invalid s scalar: %w", err)
	}
	r, _, err := parseScalar(proof.DLEQ.R)
	if err != nil {
		return fmt.Errorf("invalid r scalar: %w", err)
	}
	cBytes, err := hex.DecodeString(proof.C)
	if err != nil {
		return fmt.Errorf("invalid C point: %w", err)
	}
	c, err := secp256k1.ParsePubKey(cBytes)
	if err != nil {
		return fmt.Errorf("invalid C point: %w", err)
	}
	y, err := cashuHashToCurve([]byte(proof.Secret))
	if err != nil {
		return err
	}

	rG, err := scalarBasePoint(r)
	if err != nil {
		return err
	}
	blindedMessage, err := addPoints(y, rG)
	if err != nil {
		return err
	}
	rA, err := scalarPoint(r, mintKey)
	if err != nil {
		return err
	}
	blindedSignature, err := addPoints(c, rA)
	if err != nil {
		return err
	}

	sG, err := scalarBasePoint(s)
	if err != nil {
		return err
	}
	negE := new(secp256k1.ModNScalar)
	negE.NegateVal(e)
	negEA, err := scalarPoint(negE, mintKey)
	if err != nil {
		return err
	}
	r1, err := addPoints(sG, negEA)
	if err != nil {
		return err
	}

	sB, err := scalarPoint(s, blindedMessage)
	if err != nil {
		return err
	}
	negEC, err := scalarPoint(negE, blindedSignature)
	if err != nil {
		return err
	}
	r2, err := addPoints(sB, negEC)
	if err != nil {
		return err
	}
	challenge := cashuDLEQChallenge(r1, r2, mintKey, blindedSignature)
	if !bytes.Equal(eBytes, challenge[:]) {
		return fmt.Errorf("challenge mismatch")
	}
	return nil
}

func parseScalar(raw string) (*secp256k1.ModNScalar, []byte, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(decoded) != 32 {
		return nil, nil, fmt.Errorf("scalar must be 32-byte hex")
	}
	var scalar secp256k1.ModNScalar
	if overflow := scalar.SetByteSlice(decoded); overflow || scalar.IsZero() {
		return nil, nil, fmt.Errorf("scalar is zero or outside curve order")
	}
	return &scalar, decoded, nil
}

func cashuHashToCurve(message []byte) (*secp256k1.PublicKey, error) {
	seedInput := make([]byte, 0, len(cashuHashToCurveDomain)+len(message))
	seedInput = append(seedInput, cashuHashToCurveDomain...)
	seedInput = append(seedInput, message...)
	seed := sha256.Sum256(seedInput)
	for counter := uint32(0); counter < 1<<16; counter++ {
		var suffix [4]byte
		binary.LittleEndian.PutUint32(suffix[:], counter)
		candidateInput := append(append([]byte(nil), seed[:]...), suffix[:]...)
		candidate := sha256.Sum256(candidateInput)
		compressed := append([]byte{0x02}, candidate[:]...)
		if point, err := secp256k1.ParsePubKey(compressed); err == nil && point.IsOnCurve() {
			return point, nil
		}
	}
	return nil, fmt.Errorf("Cashu hash-to-curve failed")
}

func scalarBasePoint(scalar *secp256k1.ModNScalar) (*secp256k1.PublicKey, error) {
	var point secp256k1.JacobianPoint
	secp256k1.ScalarBaseMultNonConst(scalar, &point)
	return publicFromJacobian(&point)
}

func scalarPoint(scalar *secp256k1.ModNScalar, point *secp256k1.PublicKey) (*secp256k1.PublicKey, error) {
	var input, output secp256k1.JacobianPoint
	point.AsJacobian(&input)
	secp256k1.ScalarMultNonConst(scalar, &input, &output)
	return publicFromJacobian(&output)
}

func addPoints(left, right *secp256k1.PublicKey) (*secp256k1.PublicKey, error) {
	var a, b, result secp256k1.JacobianPoint
	left.AsJacobian(&a)
	right.AsJacobian(&b)
	secp256k1.AddNonConst(&a, &b, &result)
	return publicFromJacobian(&result)
}

func publicFromJacobian(point *secp256k1.JacobianPoint) (*secp256k1.PublicKey, error) {
	if point.Z.IsZero() {
		return nil, fmt.Errorf("invalid point at infinity")
	}
	point.ToAffine()
	return secp256k1.NewPublicKey(&point.X, &point.Y), nil
}

func cashuDLEQChallenge(points ...*secp256k1.PublicKey) [32]byte {
	var encoded strings.Builder
	for _, point := range points {
		encoded.WriteString(hex.EncodeToString(point.SerializeUncompressed()))
	}
	return sha256.Sum256([]byte(encoded.String()))
}
