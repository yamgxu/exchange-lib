package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Qitmeer/exchange-lib/address"
	"github.com/Qitmeer/exchange-lib/exchange/db"
	"github.com/Qitmeer/exchange-lib/sign"
	"github.com/Qitmeer/exchange-lib/sync"
	"github.com/Qitmeer/qitmeer/core/types"
	"github.com/bCoder778/log"
	"strconv"
)

type Api struct {
	rest         *RestApi
	storage      *db.UTXODB
	synchronizer *sync.Synchronizer
}

func NewApi(listen string, db *db.UTXODB, synchronizer *sync.Synchronizer) (*Api, error) {
	return &Api{
		rest:         NewRestApi(listen),
		storage:      db,
		synchronizer: synchronizer,
	}, nil
}

func (a *Api) Run() error {
	a.addApi()
	return a.rest.Start()
}

func (a *Api) Stop() {
	a.rest.Stop()
}

func (a *Api) addApi() {
	a.rest.AuthRouteSet("api/v1/utxo").Get(a.getUTXO)
	a.rest.AuthRouteSet("api/v1/utxo/lock").Get(a.getLockUTXO)
	a.rest.AuthRouteSet("api/v1/utxo/spent").Get(a.getSpentUTXO)
	a.rest.AuthRouteSet("api/v1/utxo").Post(a.updateUTXO)
	a.rest.AuthRouteSet("api/v1/transaction").Post(a.sendTransaction)
	a.rest.AuthRouteSet("api/v1/address").Post(a.addAddress)
	a.rest.AuthRouteSet("api/v1/address").Get(a.getAddress)
	a.rest.AuthRouteSet("api/v1/address/utxo").Get(a.getAddressUTXO)
	a.rest.AuthRouteSet("api/v1/generationAddress").Get(a.generationAddress)

	a.rest.AuthRouteSet("api/v2/transaction").Post(a.sendTransactionV2)
}

func (a *Api) getUTXO(ct *Context) (interface{}, *Error) {
	addr, ok := ct.Query["address"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "address is required"}
	}
	coin, ok := ct.Query["coin"]
	if coin == "" {
		coin = "MEER"
	}

	chainMainHeight, err := a.storage.GetHeight()
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, "no chain height"}
	}
	utxos, balance, _ := a.storage.GetAddressUTXOs(addr, coin, chainMainHeight)
	rs := map[string]interface{}{
		"utxo":    utxos,
		"balance": balance,
	}
	return rs, nil
}

func (a *Api) getSpentUTXO(ct *Context) (interface{}, *Error) {
	addr, ok := ct.Query["address"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "address is required"}
	}
	coin, ok := ct.Query["coin"]
	if coin == "" {
		coin = "MEER"
	}
	spent, amount, _ := a.storage.GetAddressSpentUTXOs(addr, coin)
	rs := map[string]interface{}{
		"spent":  spent,
		"amount": amount,
	}
	return rs, nil
}

func (a *Api) getLockUTXO(ct *Context) (interface{}, *Error) {
	addr, ok := ct.Query["address"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "address is required"}
	}
	coin, ok := ct.Query["coin"]
	if coin == "" {
		coin = "MEER"
	}
	chainMainHeight, err := a.storage.GetHeight()
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, "no chain height"}
	}
	spent, amount, _ := a.storage.GetAddressLockUTXOs(addr, coin, chainMainHeight)
	rs := map[string]interface{}{
		"spent":  spent,
		"amount": amount,
	}
	return rs, nil
}

func (a *Api) updateUTXO(ct *Context) (interface{}, *Error) {
	txid, _ := ct.Form["txid"]
	if len(txid) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "txid is required"}
	}
	vout, _ := ct.Form["vout"]
	if len(vout) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "vout is required"}
	}
	amount, _ := ct.Form["amount"]
	if len(amount) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "amount is required"}
	}
	coin, _ := ct.Form["coin"]
	if len(coin) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "coin is required"}
	}
	lock, _ := ct.Form["lock"]
	if len(amount) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "lock is required"}
	}
	address, _ := ct.Form["address"]
	if len(address) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "address is required"}
	}
	spent, _ := ct.Form["spent"]
	iVout, err := strconv.ParseUint(vout, 10, 64)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, "wrong vout"}
	}
	iAmount, err := strconv.ParseUint(amount, 10, 64)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, "wrong amount"}
	}
	iLock, err := strconv.ParseUint(lock, 10, 64)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, "wrong amount"}
	}
	err = a.storage.UpdateAddressUTXOMandatory(address, &db.UTXO{
		TxId:    txid,
		Vout:    iVout,
		Address: address,
		Coin:    coin,
		Amount:  iAmount,
		Spent:   spent,
		Lock:    iLock,
	})
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	return true, nil
}

type Utxo struct {
}

func (a *Api) sendTransaction(ct *Context) (interface{}, *Error) {
	raw, ok := ct.Form["raw"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "raw is required"}
	}
	utxos, ok := ct.Form["spent"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "spent is required"}
	}
	utxoList := []*db.UTXO{}
	err := json.Unmarshal([]byte(utxos), &utxoList)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	txId, err := a.synchronizer.SendTx(raw)
	if err == nil {

		for _, utxo := range utxoList {
			utxo.Spent = txId
			a.storage.UpdateAddressUTXO(utxo.Address, utxo)
		}
		spentUtxo := &db.SpentUTXO{
			SpentTxId: txId,
			UTXOList:  utxoList,
		}
		a.storage.InsertSpentUTXO(spentUtxo)
	} else {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	return txId, nil
}

func (a *Api) sendTransactionV2(ct *Context) (interface{}, *Error) {
	raw, ok := ct.Form["raw"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "raw is required"}
	}
	tx, err := TxDecode(raw)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	txId, err := a.synchronizer.SendTx(raw)
	if err == nil {
		decodeUtxoList := []*db.UTXO{}
		for _, vin := range tx.TxIn {
			spentedTxId := vin.PreviousOut.Hash.String()
			vout := vin.PreviousOut.OutIndex
			utxo, err := a.storage.GetUTXO(spentedTxId, uint64(vout))
			if err != nil {
				log.Errorf("can not found txid=%s, vout=%d", spentedTxId, vout)
				continue
			}
			utxo.Spent = txId
			a.storage.UpdateAddressUTXO(utxo.Address, utxo)
			decodeUtxoList = append(decodeUtxoList, utxo)
			log.Debugf("update txid=%s vout=%d spent", spentedTxId, vout)
		}
		spentUtxo := &db.SpentUTXO{
			SpentTxId: txId,
			UTXOList:  decodeUtxoList,
		}
		a.storage.InsertSpentUTXO(spentUtxo)
	} else {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	return txId, nil
}

func TxDecode(rawTxStr string) (*types.Transaction, error) {
	if len(rawTxStr)%2 != 0 {
		return nil, fmt.Errorf("invaild raw transaction : %s", rawTxStr)
	}
	serializedTx, err := hex.DecodeString(rawTxStr)
	if err != nil {
		return nil, err
	}
	var tx types.Transaction
	err = tx.Deserialize(bytes.NewReader(serializedTx))
	if err != nil {
		return nil, err
	}

	return &tx, nil
}

func (a *Api) addAddress(ct *Context) (interface{}, *Error) {
	addr, ok := ct.Form["address"]
	if !ok {
		return nil, &Error{ERROR_UNKNOWN, "address is required"}
	}
	err := a.storage.InsertAddress(addr)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	return addr, nil
}

func (a *Api) getAddress(ct *Context) (interface{}, *Error) {
	addresses := a.storage.GetAddresses()
	return addresses, nil
}

func (a *Api) getAddressUTXO(ct *Context) (interface{}, *Error) {
	address := ct.Query["address"]
	if len(address) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "address is required"}
	}
	txid := ct.Query["txid"]
	if len(txid) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "txid is required"}
	}
	vout := ct.Query["vout"]
	if len(vout) == 0 {
		return nil, &Error{ERROR_UNKNOWN, "vout is required"}
	}
	iVout, err := strconv.ParseUint(vout, 10, 64)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, "wrong vout"}
	}
	utxo, err := a.storage.GetAddressUTXO(address, txid, iVout)
	if err != nil {
		return nil, &Error{ERROR_UNKNOWN, err.Error()}
	}
	return utxo, nil
}

func (a *Api) generationAddress(ct *Context) (interface{}, *Error) {

	rest := make(map[string]string)

	ecPrivate, err := address.NewEcPrivateKey()
	if err != nil {
		return nil, nil
	}
	ecPublic, err := address.EcPrivateToPublic(ecPrivate)
	if err != nil {
		return nil, nil
	}
	address, err := address.EcPublicToAddress(ecPublic, "mainnet")
	if err != nil {
		return nil, nil
	}
	rest["address"] = address
	rest["ecPrivate"] = ecPrivate
	rest["ecPublic"] = ecPublic

	return rest, nil
}

func (a *Api) signTransaction(ct *Context) (interface{}, *Error) {
	input, _ := ct.Form["input"]
	inputValue, _ := ct.Form["inputValue"]
	pkHex, _ := ct.Form["pkHex"]
	output, _ := ct.Form["output"]
	outputValue, _ := ct.Form["outputValue"]
	key, _ := ct.Form["key"]

	inputs := make(map[string]uint32, 0)
	outputs := make(map[string]uint64, 0)
	parseInt, _ := strconv.ParseInt(inputValue, 10, 32)

	inputs[input] = uint32(parseInt)
	pkHexList := []string{pkHex}

	parseInt1, _ := strconv.ParseInt(outputValue, 10, 32)
	outputs[output] = uint64(parseInt1)

	txCode, err := sign.TxEncode(1, 0, nil, inputs, outputs, "MEER")
	if err != nil {
		fmt.Println(err)
	} else {
		rawTx, _ := sign.TxSign(txCode, []string{key}, "mainnet", pkHexList)
		return rawTx, nil
	}
	return nil, nil
}

/*
func (a *Api) signTransaction(ct *Context) (interface{}, *Error) {
	input, ok := ct.Form["inputs"]
	pkHex, ok := ct.Form["pkHex"]
	raw, ok := ct.Form["inputs"]

	rest:= make(map[string]string)
	inputs := make(map[string]uint32, 0)
	outputs := make(map[string]uint64, 0)
	inputs["fa069bd82eda6b98e9ea40a575de1dc4c053d94a9901a956e13d30f6ab81413e"] = 0
	pkHexList := []string{"76a9142a1dfad6bb26da7c0138b85440aa44a76cffade388ac"}
	outputs["TmUQjNKPA3dLBB6ZfcKd4YSDThQ9Cqzmk5S"] = 100000000
	outputs["TmWRM7fk8SzBWvuUQv2cJ4T7nWPnNmzrbxi"] = 200000000
	txCode, err := sign.TxEncode(1, 0, nil, inputs, outputs, "MEER")
	if err != nil {
		fmt.Println(err)
	} else {
		key := "1234567812345678123456781234567812345678123456781234567812345678"
		rawTx, ok := sign.TxSign(txCode, []string{key}, "testnet",  pkHexList)

	}
	return	rest, nil
}
*/
